// ── Enrolled servers (relay mode) ───────────────────────────────────────────

function escText(s) {
  const d = document.createElement('div');
  d.textContent = s == null ? '' : String(s);
  return d.innerHTML;
}

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
  rows.innerHTML = shown.map(s => {
    const badge = s.TunnelUp
      ? '<span class="badge badge-green">up</span>'
      : '<span class="badge badge-dim">down</span>';
    return `<tr>
      <td>${escText(s.server_id)}</td>
      <td>${escText(s.Path)}</td>
      <td>${escText(s.remote_port)}</td>
      <td>${escText(fmtEnrolled(s.enrolled_at))}</td>
      <td>${badge}</td>
      <td><button class="btn btn-danger" onclick="unenrollServer('${escText(s.server_id)}', ${Number(s.remote_port)})">Un-enroll</button></td>
    </tr>`;
  }).join('');
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

async function enrollServer() {
  const fileInput = $('#enroll-file'), btn = $('#btn-enroll');
  const errBox = $('#enroll-error'), okBox = $('#enroll-success');
  errBox.classList.add('hidden');
  okBox.classList.add('hidden');
  if (!fileInput.files.length) {
    errBox.textContent = 'Choose a tw_join_*.json file first.';
    errBox.classList.remove('hidden');
    return;
  }
  btn.disabled = true;
  btn.textContent = 'Enrolling…';
  try {
    const form = new FormData();
    form.append('request', fileInput.files[0]);
    const resp = await fetch('/api/servers/enroll', { method: 'POST', body: form });
    if (!resp.ok) throw new Error(await resp.text());
    const blob = await resp.blob();
    const dispo = resp.headers.get('Content-Disposition') || '';
    const m = dispo.match(/filename="?([^";]+)"?/);
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = m ? m[1] : 'tw_join_response.json';
    a.click();
    URL.revokeObjectURL(a.href);
    okBox.textContent = 'Enrolled. The join response downloaded — send it to the server and apply it there.';
    okBox.classList.remove('hidden');
    fileInput.value = '';
    loadServers();
  } catch (err) {
    errBox.textContent = err.message;
    errBox.classList.remove('hidden');
  } finally {
    btn.disabled = false;
    btn.textContent = 'Enroll';
  }
}

document.addEventListener('DOMContentLoaded', loadServers);
