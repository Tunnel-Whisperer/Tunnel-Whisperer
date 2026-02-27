// ── Navigation guard ────────────────────────────────────────────────────────

let relayOpInProgress = false;

window.addEventListener('beforeunload', (e) => {
  if (!relayOpInProgress) return;
  e.preventDefault();
  e.returnValue = 'A relay operation is in progress. Leaving now may orphan cloud resources (VMs, DNS records) that you will need to clean up manually.';
  return e.returnValue;
});

// ── Relay wizard state ──────────────────────────────────────────────────────

let wizardState = {
  domain: '',
  providerKey: '',
  providerName: '',
  token: '',
  awsSecretKey: '',
  region: '',
  regionName: '',
};

function wizardNext(step) {
  // Validate current step before advancing.
  if (step === 2) {
    const domain = $('#domain').value.trim();
    if (!domain) { alert('Domain is required'); return; }
    wizardState.domain = domain;
  }
  if (step === 4) {
    // Populate confirmation.
    const details = $('#confirm-details');
    const btn = $('#btn-provision');

    if (wizardState.providerKey === 'manual') {
      details.innerHTML = `
        <span class="kv-label">Domain</span><span class="kv-value">${wizardState.domain}</span>
        <span class="kv-label">Provider</span><span class="kv-value">Manual Install</span>
        <span class="kv-label">Firewall</span><span class="kv-value">ports 80, 443 only</span>
        <span class="kv-label">Software</span><span class="kv-value">Caddy + Xray + SSH (localhost-only)</span>
      `;
      btn.textContent = 'Generate Script';
      btn.onclick = generateManualScript;
    } else {
      details.innerHTML = `
        <span class="kv-label">Domain</span><span class="kv-value">${wizardState.domain}</span>
        <span class="kv-label">Provider</span><span class="kv-value">${wizardState.providerName}</span>
        <span class="kv-label">Region</span><span class="kv-value">${wizardState.regionName || wizardState.region || '(default)'}</span>
        <span class="kv-label">Instance</span><span class="kv-value">Ubuntu 24.04 (smallest tier)</span>
        <span class="kv-label">Firewall</span><span class="kv-value">ports 80, 443 only</span>
        <span class="kv-label">Software</span><span class="kv-value">Caddy + Xray + SSH (localhost-only)</span>
      `;
      btn.textContent = 'Provision';
      btn.onclick = startProvision;
    }
  }
  showStep(step);
}

function wizardBack(step) {
  showStep(step);
}

function showStep(step) {
  $$('.wizard-panel').forEach(p => p.classList.remove('active'));
  const panel = $(`#step-${step}`);
  if (panel) panel.classList.add('active');

  $$('.wizard-step').forEach(s => {
    const n = parseInt(s.dataset.step);
    s.classList.remove('active', 'done');
    if (n < step) s.classList.add('done');
    if (n === step) s.classList.add('active');
  });
}

// ── Provider selection ──────────────────────────────────────────────────────

(function initProviders() {
  if (typeof providers === 'undefined') return;
  const list = $('#provider-list');
  if (!list) return;

  providers.forEach(p => {
    const btn = document.createElement('button');
    btn.className = 'btn mb-8';
    btn.style.display = 'block';
    btn.style.width = '100%';
    btn.textContent = p.name;
    btn.onclick = () => {
      wizardState.providerKey = p.key;
      wizardState.providerName = p.name;
      buildCredFields(p);
      wizardNext(3);
    };
    list.appendChild(btn);
  });

  // Manual install option.
  const sep = document.createElement('div');
  sep.className = 'text-dim mt-16 mb-8';
  sep.style.textAlign = 'center';
  sep.textContent = '\u2014 or \u2014';
  list.appendChild(sep);

  const manualBtn = document.createElement('button');
  manualBtn.className = 'btn mb-8';
  manualBtn.style.display = 'block';
  manualBtn.style.width = '100%';
  manualBtn.textContent = 'Manual Install (your own server)';
  manualBtn.onclick = () => {
    wizardState.providerKey = 'manual';
    wizardState.providerName = 'Manual';
    wizardNext(4);
  };
  list.appendChild(manualBtn);
})();

function buildCredFields(provider) {
  const help = $('#cred-help');
  const fields = $('#cred-fields');
  help.textContent = `Generate here: ${provider.token_link}`;
  fields.innerHTML = '';

  if (provider.name === 'AWS') {
    fields.innerHTML = `
      <div class="form-group">
        <label>AWS Access Key ID</label>
        <input type="text" id="cred-token">
      </div>
      <div class="form-group">
        <label>AWS Secret Access Key</label>
        <input type="password" id="cred-secret">
      </div>
    `;
  } else {
    fields.innerHTML = `
      <div class="form-group">
        <label>${provider.token_name}</label>
        <input type="password" id="cred-token">
      </div>
    `;
  }

  // Region selector.
  if (provider.regions && provider.regions.length > 0) {
    let opts = provider.regions.map(r => `<option value="${r.key}">${r.name}</option>`).join('');
    fields.innerHTML += `
      <div class="form-group">
        <label>Region</label>
        <select id="cred-region">${opts}</select>
      </div>
    `;
  }
}

// ── Credential test ─────────────────────────────────────────────────────────

async function testCreds() {
  const btn = $('#btn-test-creds');
  const result = $('#cred-test-result');
  btn.disabled = true;
  result.className = 'hidden';

  wizardState.token = $('#cred-token').value.trim();
  const secretEl = $('#cred-secret');
  wizardState.awsSecretKey = secretEl ? secretEl.value.trim() : '';
  const regionEl = $('#cred-region');
  if (regionEl) {
    wizardState.region = regionEl.value;
    wizardState.regionName = regionEl.options[regionEl.selectedIndex].text;
  }

  try {
    await api.post('/api/relay/test-creds', {
      provider_name: wizardState.providerName,
      token: wizardState.token,
      aws_secret_key: wizardState.awsSecretKey,
    });
    wizardNext(4);
  } catch (err) {
    result.className = 'alert alert-error mt-16';
    result.textContent = err.message;
  } finally {
    btn.disabled = false;
  }
}

// ── Provisioning ────────────────────────────────────────────────────────────

async function startProvision() {
  const btn = $('#btn-provision');
  btn.disabled = true;
  relayOpInProgress = true;
  showStep(5);

  try {
    const resp = await api.post('/api/relay/provision', {
      domain: wizardState.domain,
      provider_key: wizardState.providerKey,
      provider_name: wizardState.providerName,
      token: wizardState.token,
      aws_secret_key: wizardState.awsSecretKey,
      region: wizardState.region,
    });

    const log = $('#provision-progress');
    connectSSE(resp.session_id, (event) => {
      renderProgressEvent(log, event);

      // Show DNS setup card when step 7 completes (relay IP known).
      if (event.step === 7 && event.status === 'completed' && event.data) {
        showDNSSetupCard(wizardState.domain, event.data);
      }
      // Also trigger on the dns_setup data event from WaitForDNS.
      if (event.data && typeof event.data === 'string' && event.data.startsWith('dns_setup:')) {
        const parts = event.data.split(':');
        if (parts.length >= 3) {
          showDNSSetupCard(parts[1], parts.slice(2).join(':'));
        }
      }
      // Hide DNS card once step 8 completes.
      if (event.step === 8 && event.status === 'completed') {
        const card = $('#dns-setup-card');
        if (card) card.classList.add('hidden');
      }
    }, (err) => {
      relayOpInProgress = false;
      if (err) {
        $('#provision-error-msg').textContent = err.message;
        $('#provision-error').classList.remove('hidden');
      } else {
        $('#provision-done').classList.remove('hidden');
      }
    });
  } catch (err) {
    relayOpInProgress = false;
    $('#provision-error-msg').textContent = err.message;
    $('#provision-error').classList.remove('hidden');
  }
}

// ── Destroy ─────────────────────────────────────────────────────────────────

function showDestroyPrompt() {
  if (!confirm('Are you sure you want to destroy the relay? This cannot be undone.')) return;

  // AWS needs credentials re-entered; other providers have them in terraform.tfvars.
  if (typeof relayProvider !== 'undefined' && relayProvider === 'AWS') {
    const panel = $('#destroy-creds');
    if (panel) panel.classList.remove('hidden');
    const btn = $('#btn-destroy');
    if (btn) btn.classList.add('hidden');
  } else {
    destroyRelay();
  }
}

function hideDestroyPrompt() {
  const panel = $('#destroy-creds');
  if (panel) panel.classList.add('hidden');
  const btn = $('#btn-destroy');
  if (btn) btn.classList.remove('hidden');
}

async function destroyRelay() {
  const btn = $('#btn-destroy-confirm') || $('#btn-destroy');
  if (btn) btn.disabled = true;
  relayOpInProgress = true;

  const credsPanel = $('#destroy-creds');
  if (credsPanel) credsPanel.classList.add('hidden');

  const log = $('#destroy-progress');
  log.classList.remove('hidden');
  log.innerHTML = '';

  // Collect AWS creds if provided.
  let creds = null;
  const keyEl = $('#destroy-aws-key');
  const secretEl = $('#destroy-aws-secret');
  if (keyEl && secretEl && keyEl.value.trim() && secretEl.value.trim()) {
    creds = {
      'AWS_ACCESS_KEY_ID': keyEl.value.trim(),
      'AWS_SECRET_ACCESS_KEY': secretEl.value.trim(),
    };
  }

  try {
    const resp = await api.post('/api/relay/destroy', { creds });
    connectSSE(resp.session_id, (event) => {
      renderProgressEvent(log, event);
    }, (err) => {
      relayOpInProgress = false;
      if (err) {
        log.innerHTML += `<div class="progress-step failed"><span class="step-label">Error: ${err.message}</span></div>`;
      } else {
        setTimeout(() => { window.location.href = '/relay'; }, 1500);
      }
    });
  } catch (err) {
    relayOpInProgress = false;
    log.innerHTML = `<div class="progress-step failed"><span class="step-label">Error: ${err.message}</span></div>`;
    if (btn) btn.disabled = false;
  }
}

// ── DNS setup card ──────────────────────────────────────────────────────────

function showDNSSetupCard(domain, ip) {
  const card = $('#dns-setup-card');
  if (!card) return;
  const domainEl = $('#dns-domain');
  const ipEl = $('#dns-ip');
  if (domainEl) domainEl.textContent = domain;
  if (ipEl) ipEl.textContent = ip;
  card.classList.remove('hidden');
}

// ── Copy relay IP ────────────────────────────────────────────────────────────

function copyRelayIP() {
  const ip = $('#dns-ip') || $('#relay-ip-value');
  if (!ip) return;
  navigator.clipboard.writeText(ip.textContent).then(() => {
    const btn = $('#btn-copy-ip');
    if (!btn) return;
    const orig = btn.textContent;
    btn.textContent = 'Copied!';
    setTimeout(() => { btn.textContent = orig; }, 1500);
  });
}

// ── Test relay connectivity ──────────────────────────────────────────────────

async function testRelay() {
  const btn = $('#btn-test-relay');
  const result = $('#test-result');
  if (!btn || !result) return;

  btn.disabled = true;
  btn.textContent = 'Testing...';
  result.innerHTML = '';
  result.className = 'progress-log mt-16';

  try {
    const { session_id } = await api.post('/api/relay/test', {});
    connectSSE(session_id, (ev) => {
      renderProgressEvent(result, ev);
    }, (err) => {
      if (err) {
        result.innerHTML += `<div class="progress-step failed"><span class="step-label">${err.message}</span></div>`;
      }
      btn.disabled = false;
      btn.textContent = 'Test Connectivity';
    });
  } catch (err) {
    result.innerHTML = `<div class="alert alert-error">${err.message}</div>`;
    btn.disabled = false;
    btn.textContent = 'Test Connectivity';
  }
}

// ── Manual install ──────────────────────────────────────────────────────────

async function generateManualScript() {
  const btn = $('#btn-provision');
  btn.disabled = true;
  showStep(5);

  try {
    const resp = await api.post('/api/relay/generate-script', {
      domain: wizardState.domain,
    });

    $('#provision-progress').classList.add('hidden');
    const result = $('#manual-result');
    result.classList.remove('hidden');
    $('#manual-script').textContent = resp.script;
    $('#manual-domain').textContent = wizardState.domain;
    window._manualScript = resp.script;
  } catch (err) {
    $('#provision-error-msg').textContent = err.message;
    $('#provision-error').classList.remove('hidden');
  }
}

function copyScript() {
  if (!window._manualScript) return;
  navigator.clipboard.writeText(window._manualScript).then(() => {
    const btn = document.querySelector('#manual-result .btn-primary');
    if (btn) {
      const orig = btn.textContent;
      btn.textContent = 'Copied!';
      setTimeout(() => { btn.textContent = orig; }, 1500);
    }
  });
}

function downloadScript() {
  if (!window._manualScript) return;
  const blob = new Blob([window._manualScript], { type: 'text/x-shellscript' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = 'install-relay.sh';
  a.click();
  URL.revokeObjectURL(url);
}

async function saveManualRelay() {
  const ip = $('#manual-ip').value.trim();
  if (!ip) { alert('IP address is required'); return; }

  const errEl = $('#manual-save-error');
  errEl.classList.add('hidden');

  try {
    await api.post('/api/relay/save-manual', {
      domain: wizardState.domain,
      ip: ip,
    });
    window.location.href = '/relay';
  } catch (err) {
    errEl.textContent = err.message;
    errEl.classList.remove('hidden');
  }
}

// ── SSH Terminal ──────────────────────────────────────────────────────────────

let sshSocket = null;
let sshTerm = null;
let sshFit = null;
let sshResizeObserver = null;

function sshConnect() {
  const container = $('#ssh-terminal');
  const badge = $('#ssh-badge');
  const btnConnect = $('#btn-ssh-connect');
  const btnDisconnect = $('#btn-ssh-disconnect');
  if (!container) return;

  btnConnect.disabled = true;
  btnConnect.textContent = 'Connecting...';
  badge.textContent = 'connecting';
  badge.className = 'badge badge-yellow';

  // Initialize xterm.js terminal.
  sshTerm = new Terminal({
    cursorBlink: true,
    fontSize: 14,
    fontFamily: '"Fira Code", "Cascadia Code", "JetBrains Mono", monospace',
    theme: {
      background: '#0d1117',
      foreground: '#c9d1d9',
      cursor: '#58a6ff',
      selectionBackground: '#264f78',
    },
  });
  sshFit = new FitAddon.FitAddon();
  sshTerm.loadAddon(sshFit);

  container.classList.remove('hidden');
  sshTerm.open(container);
  sshFit.fit();

  // WebSocket URL — same host, ws:// or wss:// matching current protocol.
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  sshSocket = new WebSocket(`${proto}//${location.host}/api/relay/ssh`);
  sshSocket.binaryType = 'arraybuffer';

  sshSocket.onopen = () => {
    // Send initial size.
    sshSocket.send(JSON.stringify({
      type: 'resize',
      cols: sshTerm.cols,
      rows: sshTerm.rows,
    }));
  };

  sshSocket.onmessage = (e) => {
    if (typeof e.data === 'string') {
      // Control message from server.
      try {
        const msg = JSON.parse(e.data);
        if (msg.type === 'status' && msg.msg === 'connected') {
          badge.textContent = 'connected';
          badge.className = 'badge badge-green';
          btnConnect.classList.add('hidden');
          btnDisconnect.classList.remove('hidden');
          sshTerm.focus();
        } else if (msg.type === 'error') {
          sshTerm.writeln('\r\n\x1b[31mError: ' + msg.msg + '\x1b[0m');
          sshCleanup();
        }
      } catch (_) {}
    } else {
      // Binary terminal data.
      sshTerm.write(new Uint8Array(e.data));
    }
  };

  sshSocket.onclose = () => {
    sshTerm.writeln('\r\n\x1b[2m--- session closed ---\x1b[0m');
    sshCleanup();
  };

  sshSocket.onerror = () => {
    sshCleanup();
  };

  // Terminal input → WebSocket.
  sshTerm.onData((data) => {
    if (sshSocket && sshSocket.readyState === WebSocket.OPEN) {
      const encoder = new TextEncoder();
      sshSocket.send(encoder.encode(data));
    }
  });

  // Handle resize.
  sshResizeObserver = new ResizeObserver(() => {
    if (sshFit) {
      sshFit.fit();
      if (sshSocket && sshSocket.readyState === WebSocket.OPEN) {
        sshSocket.send(JSON.stringify({
          type: 'resize',
          cols: sshTerm.cols,
          rows: sshTerm.rows,
        }));
      }
    }
  });
  sshResizeObserver.observe(container);
}

function sshDisconnect() {
  if (sshSocket) {
    sshSocket.close();
    sshSocket = null;
  }
}

function sshCleanup() {
  const badge = $('#ssh-badge');
  const btnConnect = $('#btn-ssh-connect');
  const btnDisconnect = $('#btn-ssh-disconnect');

  if (badge) {
    badge.textContent = 'disconnected';
    badge.className = 'badge badge-dim';
  }
  if (btnConnect) {
    btnConnect.classList.remove('hidden');
    btnConnect.disabled = false;
    btnConnect.textContent = 'Connect';
  }
  if (btnDisconnect) {
    btnDisconnect.classList.add('hidden');
  }

  if (sshResizeObserver) {
    sshResizeObserver.disconnect();
    sshResizeObserver = null;
  }

  sshSocket = null;
  // Keep terminal visible so user can see last output.
  // It will be disposed on next connect.
  if (sshTerm) {
    sshTerm.dispose();
    sshTerm = null;
    sshFit = null;
    const container = $('#ssh-terminal');
    if (container) container.classList.add('hidden');
  }
}
