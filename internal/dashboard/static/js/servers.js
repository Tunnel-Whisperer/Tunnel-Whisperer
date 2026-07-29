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

async function loadServers() {
  const loading = $('#servers-loading'), errBox = $('#servers-error');
  const table = $('#servers-table'), rows = $('#servers-rows'), empty = $('#servers-empty');
  loading.classList.remove('hidden');
  errBox.classList.add('hidden');
  table.classList.add('hidden');
  empty.classList.add('hidden');
  try {
    const list = await api.get('/api/servers');
    loading.classList.add('hidden');
    if (!list.length) { empty.classList.remove('hidden'); return; }
    rows.innerHTML = list.map(s => {
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
    table.classList.remove('hidden');
  } catch (err) {
    loading.classList.add('hidden');
    errBox.textContent = err.message;
    errBox.classList.remove('hidden');
  }
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
