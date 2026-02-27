// ── Server start/stop ────────────────────────────────────────────────────────

async function serverStart() {
  const btn = $('#btn-server-start');
  if (btn) btn.disabled = true;

  const log = $('#server-progress');
  log.classList.remove('hidden');
  log.innerHTML = '';

  try {
    const { session_id } = await api.post('/api/server/start', {});
    connectSSE(session_id, (ev) => renderProgressEvent(log, ev), (err) => {
      if (err) {
        log.innerHTML += `<div class="progress-step failed"><span class="step-label">${err.message}</span></div>`;
      }
      setTimeout(() => window.location.reload(), 1000);
    });
  } catch (e) {
    log.innerHTML = `<div class="alert alert-error">${e.message}</div>`;
    if (btn) btn.disabled = false;
  }
}

async function serverStop() {
  const btn = $('#btn-server-stop');
  if (btn) btn.disabled = true;

  const log = $('#server-progress');
  log.classList.remove('hidden');
  log.innerHTML = '';

  try {
    const { session_id } = await api.post('/api/server/stop', {});
    connectSSE(session_id, (ev) => renderProgressEvent(log, ev), () => {
      setTimeout(() => window.location.reload(), 1000);
    });
  } catch (e) {
    log.innerHTML = `<div class="alert alert-error">${e.message}</div>`;
    if (btn) btn.disabled = false;
  }
}

async function serverRestart() {
  const btn = $('#btn-server-restart');
  if (btn) btn.disabled = true;

  const log = $('#server-progress');
  log.classList.remove('hidden');
  log.innerHTML = '';

  try {
    const { session_id } = await api.post('/api/server/restart', {});
    connectSSE(session_id, (ev) => renderProgressEvent(log, ev), (err) => {
      if (err) {
        log.innerHTML += `<div class="progress-step failed"><span class="step-label">${err.message}</span></div>`;
      }
      setTimeout(() => window.location.reload(), 1000);
    });
  } catch (e) {
    log.innerHTML = `<div class="alert alert-error">${e.message}</div>`;
    if (btn) btn.disabled = false;
  }
}

// ── Client start/stop ────────────────────────────────────────────────────────

async function clientStart() {
  const btn = $('#btn-client-start');
  if (btn) btn.disabled = true;

  const log = $('#client-progress');
  log.classList.remove('hidden');
  log.innerHTML = '';

  try {
    const { session_id } = await api.post('/api/client/start', {});
    connectSSE(session_id, (ev) => renderProgressEvent(log, ev), (err) => {
      if (err) {
        log.innerHTML += `<div class="progress-step failed"><span class="step-label">${err.message}</span></div>`;
      }
      setTimeout(() => window.location.reload(), 1000);
    });
  } catch (e) {
    log.innerHTML = `<div class="alert alert-error">${e.message}</div>`;
    if (btn) btn.disabled = false;
  }
}

async function clientStop() {
  const btn = $('#btn-client-stop');
  if (btn) btn.disabled = true;

  const log = $('#client-progress');
  log.classList.remove('hidden');
  log.innerHTML = '';

  try {
    const { session_id } = await api.post('/api/client/stop', {});
    connectSSE(session_id, (ev) => renderProgressEvent(log, ev), () => {
      setTimeout(() => window.location.reload(), 1000);
    });
  } catch (e) {
    log.innerHTML = `<div class="alert alert-error">${e.message}</div>`;
    if (btn) btn.disabled = false;
  }
}

async function clientReconnect() {
  const btn = $('#btn-client-reconnect');
  if (btn) btn.disabled = true;

  const log = $('#client-progress');
  log.classList.remove('hidden');
  log.innerHTML = '';

  try {
    const { session_id } = await api.post('/api/client/reconnect', {});
    connectSSE(session_id, (ev) => renderProgressEvent(log, ev), (err) => {
      if (err) {
        log.innerHTML += `<div class="progress-step failed"><span class="step-label">${err.message}</span></div>`;
      }
      setTimeout(() => window.location.reload(), 1000);
    });
  } catch (e) {
    log.innerHTML = `<div class="alert alert-error">${e.message}</div>`;
    if (btn) btn.disabled = false;
  }
}

// ── Status polling ──────────────────────────────────────────────────────────

(function() {
  function setStatus(bind, text, cls) {
    const el = document.querySelector(`[data-bind="${bind}"]`);
    if (!el) return;
    el.textContent = text;
    el.classList.remove('status-up', 'status-down', 'status-error');
    if (cls) el.classList.add(cls);
  }

  function setError(bind, text) {
    const el = document.querySelector(`[data-bind="${bind}"]`);
    if (!el) return;
    if (text) {
      el.textContent = text;
      el.classList.remove('hidden');
    } else {
      el.classList.add('hidden');
    }
  }

  function setBadge(bind, text) {
    const el = document.querySelector(`[data-bind="${bind}"]`);
    if (!el) return;
    el.textContent = text;
    el.className = 'badge badge-state';
    if (text === 'running') el.classList.add('badge-green');
    else if (text === 'error') el.classList.add('badge-red');
    else if (text === 'stopped') el.classList.add('badge-dim');
    else el.classList.add('badge-yellow');
  }

  async function poll() {
    try {
      const s = await api.get('/api/status');

      if (s.server) {
        setBadge('server-badge', s.server.state);
        setStatus('srv-ssh', s.server.ssh ? 'up' : 'down', s.server.ssh ? 'status-up' : 'status-down');
        setStatus('srv-xray', s.server.xray ? 'up' : 'down', s.server.xray ? 'status-up' : 'status-down');
        const tunText = s.server.tunnel ? 'up' : s.server.tunnel_error ? 'error' : 'down';
        const tunCls = s.server.tunnel ? 'status-up' : 'status-down';
        setStatus('srv-tunnel', tunText, s.server.tunnel_error ? 'status-error' : tunCls);
        setError('srv-tunnel-error', s.server.tunnel_error || '');
        setError('srv-error', s.server.error || '');
      }

      if (s.client) {
        setBadge('client-badge', s.client.state);
        setStatus('cli-xray', s.client.xray ? 'up' : 'down', s.client.xray ? 'status-up' : 'status-down');
        const tunText = s.client.tunnel ? 'up' : s.client.tunnel_error ? 'error' : 'down';
        const tunCls = s.client.tunnel ? 'status-up' : 'status-down';
        setStatus('cli-tunnel', tunText, s.client.tunnel_error ? 'status-error' : tunCls);
        setError('cli-tunnel-error', s.client.tunnel_error || '');
        setError('cli-error', s.client.error || '');

        const tunBadge = document.querySelector('[data-bind="tunnel-badge"]');
        if (tunBadge) {
          if (s.client.tunnel) {
            tunBadge.textContent = 'connected';
            tunBadge.className = 'badge badge-green';
          } else {
            tunBadge.textContent = 'disconnected';
            tunBadge.className = 'badge badge-dim';
          }
        }
      }

      // Update Clients card online status.
      const onlineSet = new Set(s.online || []);
      const onlineCount = onlineSet.size;

      const countEl = document.querySelector('[data-bind="online-count"]');
      if (countEl) {
        countEl.textContent = onlineCount + ' online';
        countEl.className = 'badge ' + (onlineCount > 0 ? 'badge-green' : 'badge-dim');
      }

      const userCountEl = document.querySelector('[data-bind="user-count"]');
      if (userCountEl && s.user_count !== undefined) {
        userCountEl.textContent = s.user_count + ' total';
      }

      const userList = document.getElementById('user-list');
      if (userList) {
        const rows = Array.from(userList.querySelectorAll('.user-row[data-uuid]'));
        rows.forEach(row => {
          const uuid = row.dataset.uuid;
          const badge = row.querySelector('.user-online-badge');
          if (!badge) return;
          if (onlineSet.has(uuid)) {
            badge.className = 'badge badge-green user-online-badge';
            badge.classList.remove('hidden');
          } else {
            badge.className = 'badge badge-dim user-online-badge hidden';
          }
        });

        // Re-sort: online users first, then alphabetical.
        rows.sort((a, b) => {
          const aOn = onlineSet.has(a.dataset.uuid) ? 0 : 1;
          const bOn = onlineSet.has(b.dataset.uuid) ? 0 : 1;
          if (aOn !== bOn) return aOn - bOn;
          return a.textContent.trim().localeCompare(b.textContent.trim());
        });
        rows.forEach(row => userList.appendChild(row));
      }

      // Config change indicator.
      const cfgAlert = document.getElementById('config-changed');
      if (cfgAlert) {
        if (s.config_changed) {
          const action = s.mode === 'client' ? 'Reconnect' : 'Restart';
          cfgAlert.textContent = 'Configuration has changed. ' + action + ' to apply.';
          cfgAlert.classList.remove('hidden');
        } else {
          cfgAlert.classList.add('hidden');
        }
      }
    } catch (_) {}
  }

  setInterval(poll, 3000);
  poll();
})();

// ── Console log streaming ───────────────────────────────────────────────────

(function() {
  const el = $('#console-log');
  if (!el) return;

  const source = new EventSource('/api/logs');
  source.onmessage = (e) => {
    const entry = JSON.parse(e.data);
    const line = document.createElement('div');
    line.className = 'log-line';
    line.innerHTML =
      `<span class="log-time">${entry.time}</span>` +
      `<span class="log-level log-level-${entry.level}">${entry.level}</span>` +
      `<span class="log-msg">${escapeHtml(entry.msg)}</span>`;
    el.appendChild(line);
    el.scrollTop = el.scrollHeight;
  };
  source.onerror = () => {
    // Reconnects automatically via EventSource.
  };
})();

function clearConsole() {
  const el = $('#console-log');
  if (el) el.innerHTML = '';
}

function escapeHtml(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

function copyText(text, el) {
  navigator.clipboard.writeText(text).then(() => {
    const orig = el.textContent;
    el.textContent = 'copied!';
    setTimeout(() => { el.textContent = orig; }, 1000);
  });
}

// ── Config zip upload ────────────────────────────────────────────────────────

(function() {
  const form = $('#upload-form');
  if (!form) return;

  const fileInput = $('#config-file');
  const uploadArea = $('#upload-area');
  const filenameEl = $('#upload-filename');
  const uploadBtn = $('#btn-upload');
  const errorEl = $('#upload-error');

  fileInput.addEventListener('change', () => {
    if (fileInput.files.length > 0) {
      filenameEl.textContent = fileInput.files[0].name;
      uploadBtn.disabled = false;
    }
  });

  // Drag and drop.
  uploadArea.addEventListener('dragover', (e) => {
    e.preventDefault();
    uploadArea.classList.add('dragover');
  });
  uploadArea.addEventListener('dragleave', () => {
    uploadArea.classList.remove('dragover');
  });
  uploadArea.addEventListener('drop', (e) => {
    e.preventDefault();
    uploadArea.classList.remove('dragover');
    if (e.dataTransfer.files.length > 0) {
      fileInput.files = e.dataTransfer.files;
      filenameEl.textContent = e.dataTransfer.files[0].name;
      uploadBtn.disabled = false;
    }
  });

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    errorEl.classList.add('hidden');
    uploadBtn.disabled = true;
    uploadBtn.textContent = 'Uploading...';

    const fd = new FormData();
    fd.append('config', fileInput.files[0]);

    try {
      const resp = await fetch('/api/client/upload', { method: 'POST', body: fd });
      if (!resp.ok) {
        const data = await resp.json();
        throw new Error(data.error || 'Upload failed');
      }
      window.location.reload();
    } catch (err) {
      errorEl.textContent = err.message;
      errorEl.classList.remove('hidden');
      uploadBtn.disabled = false;
      uploadBtn.textContent = 'Upload Config';
    }
  });
})();
