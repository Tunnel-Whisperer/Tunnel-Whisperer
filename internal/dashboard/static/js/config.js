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
