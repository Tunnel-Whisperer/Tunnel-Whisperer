// ── Port warnings ──────────────────────────────────────────────────────────

const KNOWN_PORTS = {
  21: 'FTP', 22: 'SSH', 23: 'Telnet', 25: 'SMTP', 53: 'DNS',
  80: 'HTTP', 110: 'POP3', 143: 'IMAP', 443: 'HTTPS', 445: 'SMB',
  993: 'IMAPS', 995: 'POP3S', 3306: 'MySQL', 5432: 'PostgreSQL',
};

let portWarnTimer = null;

function checkPortWarning(e) {
  const input = e.target;
  const row = input.closest('.mapping-row');
  const existing = row.nextElementSibling;
  const warn = existing && existing.classList.contains('port-warning') ? existing : null;

  const port = parseInt(input.value);

  if (!port || port > 1023) {
    clearTimeout(portWarnTimer);
    if (warn) warn.remove();
    return;
  }

  clearTimeout(portWarnTimer);
  portWarnTimer = setTimeout(() => {
    const service = KNOWN_PORTS[port] || null;
    let msg = `Port ${port} is a privileged port (below 1024) — it may require root/admin privileges on the client.`;
    if (service) msg = `Port ${port} is commonly used by ${service}. ` + msg;

    let el = row.nextElementSibling;
    if (!el || !el.classList.contains('port-warning')) {
      el = document.createElement('div');
      el.className = 'port-warning alert alert-warning mt-8';
      el.style.fontSize = '13px';
      row.after(el);
    }
    el.textContent = msg;
  }, 600);
}

document.addEventListener('DOMContentLoaded', () => {
  document.querySelectorAll('.mapping-row .client-port').forEach(input => {
    input.addEventListener('input', checkPortWarning);
  });
  updateRemoveButtons();
});

// ── Mapping editor (shared pattern) ─────────────────────────────────────────

function addMapping() {
  const container = $('#mappings');
  const row = document.createElement('div');
  row.className = 'mapping-row';
  row.innerHTML = `
    <input type="number" class="client-port" placeholder="Client port" min="1" max="65535">
    <span class="arrow">-></span>
    <input type="number" class="server-port" placeholder="Server port" min="1" max="65535">
    <button class="btn btn-sm btn-danger" onclick="removeMapping(this)">x</button>
  `;
  container.appendChild(row);
  row.querySelector('.client-port').addEventListener('input', checkPortWarning);
  updateRemoveButtons();
}

function removeMapping(btn) {
  const row = btn.closest('.mapping-row');
  const warn = row.nextElementSibling;
  if (warn && warn.classList.contains('port-warning')) warn.remove();
  row.remove();
  updateRemoveButtons();
}

function updateRemoveButtons() {
  const rows = $$('.mapping-row');
  rows.forEach(row => {
    const btn = row.querySelector('.btn-danger');
    btn.style.visibility = rows.length > 1 ? 'visible' : 'hidden';
  });
}

function getMappings() {
  return $$('.mapping-row').map(row => {
    const cp = row.querySelector('.client-port').value.trim();
    const sp = row.querySelector('.server-port').value.trim();
    if (!cp || !sp) return null;
    return { client_port: parseInt(cp), server_port: parseInt(sp) };
  }).filter(Boolean);
}

// ── Create application ─────────────────────────────────────────────────────

async function createApp() {
  const name = $('#app-name').value.trim();
  if (!name) { alert('Application name is required'); return; }
  if (!/^[a-zA-Z0-9_-]+$/.test(name)) { alert('Name must contain only letters, numbers, dashes, and underscores'); return; }

  const mappings = getMappings();
  if (mappings.length === 0) { alert('At least one port mapping is required'); return; }

  const btn = $('#btn-create-app');
  btn.disabled = true;

  try {
    await api.post('/api/apps', { name, mappings });
    window.location.href = '/apps';
  } catch (err) {
    alert('Error: ' + err.message);
    btn.disabled = false;
  }
}

// ── Update application ────────────────────────────────────────────────────

async function updateApp(originalName) {
  const name = $('#app-name').value.trim();
  if (!name) { alert('Application name is required'); return; }
  if (!/^[a-zA-Z0-9_-]+$/.test(name)) { alert('Name must contain only letters, numbers, dashes, and underscores'); return; }

  const mappings = getMappings();
  if (mappings.length === 0) { alert('At least one port mapping is required'); return; }

  const btn = $('#btn-update-app');
  btn.disabled = true;

  try {
    await api.put(`/api/apps/${originalName}`, { name, mappings });
    window.location.href = '/apps';
  } catch (err) {
    alert('Error: ' + err.message);
    btn.disabled = false;
  }
}

// ── Delete application ─────────────────────────────────────────────────────

async function deleteApp(name) {
  if (!confirm(`Delete application "${name}"?`)) return;

  try {
    await api.del(`/api/apps/${name}`);
    window.location.reload();
  } catch (err) {
    alert('Delete failed: ' + err.message);
  }
}
