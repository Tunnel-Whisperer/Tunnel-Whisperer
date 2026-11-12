// ── Enrolled servers (relay mode) ───────────────────────────────────────────

function fmtEnrolled(iso) {
  if (!iso) return '—';
  return iso.replace('T', ' ').replace(/:\d\d(Z|[+-].*)?$/, '');
}

let allServers = [];

async function loadServers() {
  const loading = $('#servers-loading'), errBox = $('#servers-error');
  const table = $('#servers-table'), empty = $('#servers-empty');
  loading.classList.remove('hidden');
  errBox.classList.add('hidden');
  table.classList.add('hidden');
  empty.classList.add('hidden');
  try {
    allServers = (await api.get('/api/servers')) || [];
    loading.classList.add('hidden');
    if (!allServers.length) { empty.classList.remove('hidden'); return; }
    renderServers();
    table.classList.remove('hidden');
  } catch (err) {
    loading.classList.add('hidden');
    errBox.textContent = err.message;
    errBox.classList.remove('hidden');
  }
}

function renderServers() {
  const rows = $('#servers-rows'), count = $('#servers-count');
  if (!rows) return;
  const q = ($('#servers-search')?.value || '').trim().toLowerCase();
  const tunnel = $('#servers-tunnel-filter')?.value || '';
  const shown = allServers.filter(s => {
    if (tunnel === 'up' && !s.TunnelUp) return false;
    if (tunnel === 'down' && s.TunnelUp) return false;
    if (!q) return true;
    const hay = [s.server_id, s.Path, s.remote_port, fmtEnrolled(s.enrolled_at)]
      .map(v => String(v == null ? '' : v).toLowerCase()).join(' ');
    return hay.includes(q);
  });
  if (q || tunnel) {
    count.textContent = `${shown.length} of ${allServers.length}`;
    count.classList.remove('hidden');
  } else {
    count.classList.add('hidden');
  }
  if (!shown.length) {
    rows.innerHTML = '<tr><td colspan="6" class="text-dim">no matches</td></tr>';
    return;
  }
  rows.replaceChildren(...shown.map(s => {
    const tr = document.createElement('tr');
    [s.server_id, s.Path, s.remote_port, fmtEnrolled(s.enrolled_at)]
      .forEach(v => tr.appendChild(makeEl('td', '', v == null ? '' : v)));
    const badgeTd = document.createElement('td');
    badgeTd.appendChild(s.TunnelUp
      ? makeEl('span', 'badge badge-green', 'up')
      : makeEl('span', 'badge badge-dim', 'down'));
    tr.appendChild(badgeTd);
    const actionTd = document.createElement('td');
    const btn = makeEl('button', 'btn btn-danger', 'Un-enroll');
    btn.addEventListener('click', () => unenrollServer(s.server_id, Number(s.remote_port)));
    actionTd.appendChild(btn);
    tr.appendChild(actionTd);
    return tr;
  }));
}

async function unenrollServer(serverID, port) {
  if (!confirm(`Un-enroll ${serverID} (port ${port})? Its relay access and all its live connections end immediately.`)) return;
  const errBox = $('#servers-error');
  errBox.classList.add('hidden');
  try {
    const resp = await fetch('/api/servers/unenroll', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ server_id: serverID }),
    });
    if (!resp.ok) throw new Error(await resp.text());
    loadServers();
  } catch (err) {
    errBox.textContent = err.message;
    errBox.classList.remove('hidden');
  }
}

document.addEventListener('DOMContentLoaded', loadServers);
