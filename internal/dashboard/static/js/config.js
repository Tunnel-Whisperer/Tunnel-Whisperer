// ── Log level ───────────────────────────────────────────────────────────────

async function saveLogLevel() {
  const select_ = $('#log-level-select');
  const level = select_.value;
  const btn = $('#btn-log-level-save');
  btn.disabled = true;

  try {
    await api.post('/api/log-level', { log_level: level });
    const action = typeof serviceMode !== 'undefined' && serviceMode === 'client' ? 'Reconnect' : 'Restart';
    const restart = typeof serviceRunning !== 'undefined' && serviceRunning
      ? ' ' + action + ' to apply.' : '';
    showLogLevelSuccess('Log level saved.' + restart);
    updateLogLevelBadge(level);
    reloadConfigYAML();
  } catch (err) {
    showLogLevelError(err.message);
  } finally {
    btn.disabled = false;
  }
}

function updateLogLevelBadge(level) {
  const badge = $('#log-level-badge');
  if (!badge) return;
  badge.textContent = level;
  if (level === 'debug' || level === 'warn') {
    badge.className = 'badge badge-yellow';
  } else if (level === 'error') {
    badge.className = 'badge badge-red';
  } else {
    badge.className = 'badge badge-dim';
  }
}

function showLogLevelError(msg) {
  const el = $('#log-level-error');
  const ok = $('#log-level-success');
  if (ok) ok.classList.add('hidden');
  if (el) {
    el.textContent = msg;
    el.classList.remove('hidden');
  }
}

function showLogLevelSuccess(msg) {
  const el = $('#log-level-success');
  const err = $('#log-level-error');
  if (err) err.classList.add('hidden');
  if (el) {
    el.textContent = msg;
    el.classList.remove('hidden');
  }
}

// ── Proxy settings ──────────────────────────────────────────────────────────

async function saveProxy() {
  const input = $('#proxy-url');
  const url = input.value.trim();

  const btn = $('#btn-proxy-save');
  btn.disabled = true;

  try {
    await api.post('/api/proxy', { proxy: url });
    const action = typeof serviceMode !== 'undefined' && serviceMode === 'client' ? 'Reconnect' : 'Restart';
    const restart = typeof serviceRunning !== 'undefined' && serviceRunning
      ? ' ' + action + ' to apply.' : '';
    if (url) {
      showProxySuccess('Proxy saved.' + restart);
    } else {
      showProxySuccess('Proxy cleared.' + restart);
    }
    updateProxyBadge(url);
    reloadConfigYAML();
  } catch (err) {
    showProxyError(err.message);
  } finally {
    btn.disabled = false;
  }
}

async function clearProxy() {
  try {
    await api.post('/api/proxy', { proxy: '' });
    $('#proxy-url').value = '';
    const action = typeof serviceMode !== 'undefined' && serviceMode === 'client' ? 'Reconnect' : 'Restart';
    const restart = typeof serviceRunning !== 'undefined' && serviceRunning
      ? ' ' + action + ' to apply.' : '';
    showProxySuccess('Proxy cleared.' + restart);
    updateProxyBadge('');
    reloadConfigYAML();
  } catch (err) {
    showProxyError(err.message);
  }
}

function updateProxyBadge(url) {
  const badge = $('#proxy-badge');
  if (!badge) return;
  if (url) {
    badge.textContent = 'configured';
    badge.className = 'badge badge-green';
  } else {
    badge.textContent = 'none';
    badge.className = 'badge badge-dim';
  }
}

function showProxyError(msg) {
  const el = $('#proxy-error');
  const ok = $('#proxy-success');
  if (ok) ok.classList.add('hidden');
  if (el) {
    el.textContent = msg;
    el.classList.remove('hidden');
  }
}

function showProxySuccess(msg) {
  const el = $('#proxy-success');
  const err = $('#proxy-error');
  if (err) err.classList.add('hidden');
  if (el) {
    el.textContent = msg;
    el.classList.remove('hidden');
  }
}

// ── Xray settings ───────────────────────────────────────────────────────────

async function saveXraySettings() {
  const btn = $('#btn-xray-save');
  btn.disabled = true;

  try {
    await api.post('/api/settings/xray', {
      relay_host: $('#xray-relay-host').value.trim(),
      relay_port: parseInt($('#xray-relay-port').value) || 0,
      path: $('#xray-path').value.trim(),
    });
    showResult('xray', true, 'Xray settings saved.' + restartMsg());
    reloadConfigYAML();
  } catch (err) {
    showResult('xray', false, err.message);
  } finally {
    btn.disabled = false;
  }
}

// ── Server settings ─────────────────────────────────────────────────────────

async function saveServerSettings() {
  const btn = $('#btn-server-save');
  btn.disabled = true;

  try {
    await api.post('/api/settings/server', {
      ssh_port: parseInt($('#srv-ssh-port').value) || 0,
      api_port: parseInt($('#srv-api-port').value) || 0,
      dashboard_port: parseInt($('#srv-dashboard-port').value) || 0,
      relay_ssh_port: parseInt($('#srv-relay-ssh-port').value) || 0,
      relay_ssh_user: $('#srv-relay-ssh-user').value.trim(),
      remote_port: parseInt($('#srv-remote-port').value) || 0,
      temp_xray_port: parseInt($('#srv-temp-xray-port').value) || 0,
    });
    showResult('server', true, 'Server settings saved.' + restartMsg());
    reloadConfigYAML();
  } catch (err) {
    showResult('server', false, err.message);
  } finally {
    btn.disabled = false;
  }
}

// ── Client settings ─────────────────────────────────────────────────────────

async function saveClientSettings() {
  const btn = $('#btn-client-save');
  btn.disabled = true;

  try {
    await api.post('/api/settings/client', {
      ssh_user: $('#cli-ssh-user').value.trim(),
      server_ssh_port: parseInt($('#cli-server-ssh-port').value) || 0,
    });
    showResult('client', true, 'Client settings saved. Reconnect to apply.');
    reloadConfigYAML();
  } catch (err) {
    showResult('client', false, err.message);
  } finally {
    btn.disabled = false;
  }
}

// ── Helpers ─────────────────────────────────────────────────────────────────

function restartMsg() {
  if (typeof serviceRunning === 'undefined' || !serviceRunning) return '';
  const action = typeof serviceMode !== 'undefined' && serviceMode === 'client' ? ' Reconnect' : ' Restart';
  return action + ' to apply.';
}

function showResult(section, success, msg) {
  const errEl = $('#' + section + '-error');
  const okEl = $('#' + section + '-success');
  if (success) {
    if (errEl) errEl.classList.add('hidden');
    if (okEl) { okEl.textContent = msg; okEl.classList.remove('hidden'); }
  } else {
    if (okEl) okEl.classList.add('hidden');
    if (errEl) { errEl.textContent = msg; errEl.classList.remove('hidden'); }
  }
}

// Reload the config YAML block without a full page refresh.
async function reloadConfigYAML() {
  try {
    const resp = await fetch('/config');
    const html = await resp.text();
    const doc = new DOMParser().parseFromString(html, 'text/html');
    const fresh = doc.querySelector('pre');
    const current = $('pre');
    if (fresh && current) current.textContent = fresh.textContent;
  } catch (_) {
    // ignore — non-critical
  }
}
