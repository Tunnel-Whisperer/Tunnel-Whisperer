// ── Delete user ─────────────────────────────────────────────────────────────

async function deleteUser(name) {
  if (!confirm(`Delete user "${name}"? This removes their keys, config, and authorized_keys entry.`)) return;

  const btn = $('#btn-delete');
  btn.disabled = true;

  try {
    await api.del(`/api/users/${name}`);
    window.location.href = '/users';
  } catch (err) {
    alert('Delete failed: ' + err.message);
    btn.disabled = false;
  }
}

// ── Register / unregister users on relay ─────────────────────────────────────

async function applyUser(name) {
  await relayUsersRequest('/api/users/apply', { names: [name] });
}

async function applyAllUsers() {
  await relayUsersRequest('/api/users/apply', { names: [] });
}

async function unregisterUser(name) {
  if (!confirm(`Unregister "${name}" from the relay? They will lose tunnel access until re-registered.`)) return;
  await relayUsersRequest('/api/users/unregister', { names: [name] });
}

async function relayUsersRequest(endpoint, body) {
  const container = $('#apply-progress-container');
  const log = $('#apply-progress');
  if (!container || !log) return;

  container.classList.remove('hidden');
  log.innerHTML = '';

  try {
    const resp = await api.post(endpoint, body);
    connectSSE(resp.session_id, (event) => {
      renderProgressEvent(log, event);
    }, (err) => {
      if (err) {
        log.innerHTML += `<div class="progress-step failed"><span class="step-label">Error: ${err.message}</span></div>`;
      } else {
        setTimeout(() => { window.location.reload(); }, 1000);
      }
    });
  } catch (err) {
    log.innerHTML = `<div class="alert alert-error">${err.message}</div>`;
  }
}

// ── Search, Sort & Pagination ────────────────────────────────────────────────

(function() {
  const PAGE_SIZE = 10;
  let currentPage = 1;
  let sortCol = 'status';
  let sortDir = 'asc'; // asc = online/registered first

  const searchInput = $('#user-search');
  const table = $('#users-table');
  if (!searchInput || !table) return;

  const tbody = table.querySelector('tbody');
  const allRows = Array.from(tbody.querySelectorAll('tr'));
  const headers = table.querySelectorAll('th.sortable');

  // ── Sort helpers ──

  function getSortValue(row, col) {
    switch (col) {
      case 'name':    return row.dataset.user.toLowerCase();
      case 'tunnels': return parseInt(row.dataset.tunnels) || 0;
      case 'status':  return parseInt(row.dataset.status) || 0;
      default:        return '';
    }
  }

  function sortRows() {
    allRows.sort((a, b) => {
      const va = getSortValue(a, sortCol);
      const vb = getSortValue(b, sortCol);
      let cmp = 0;
      if (typeof va === 'number') {
        cmp = va - vb;
      } else {
        cmp = va.localeCompare(vb);
      }
      return sortDir === 'asc' ? cmp : -cmp;
    });
    // Re-append in sorted order.
    allRows.forEach(row => tbody.appendChild(row));
  }

  function updateHeaders() {
    headers.forEach(th => {
      th.classList.remove('active', 'sort-asc', 'sort-desc');
      if (th.dataset.sort === sortCol) {
        th.classList.add('active', sortDir === 'asc' ? 'sort-asc' : 'sort-desc');
      }
    });
  }

  headers.forEach(th => {
    th.addEventListener('click', () => {
      const col = th.dataset.sort;
      if (sortCol === col) {
        sortDir = sortDir === 'asc' ? 'desc' : 'asc';
      } else {
        sortCol = col;
        sortDir = 'asc';
      }
      updateHeaders();
      sortRows();
      currentPage = 1;
      filterAndPaginate();
    });
  });

  // ── Filter & paginate ──

  function filterAndPaginate() {
    const query = searchInput.value.toLowerCase().trim();

    const matching = [];
    allRows.forEach(row => {
      const name = row.querySelector('td').textContent.toLowerCase();
      if (name.includes(query)) {
        matching.push(row);
      }
    });

    const totalPages = Math.max(1, Math.ceil(matching.length / PAGE_SIZE));
    if (currentPage > totalPages) currentPage = totalPages;

    const start = (currentPage - 1) * PAGE_SIZE;
    const end = start + PAGE_SIZE;

    allRows.forEach(row => row.style.display = 'none');
    matching.forEach((row, i) => {
      row.style.display = (i >= start && i < end) ? '' : 'none';
    });

    renderPagination(matching.length, totalPages);
  }

  function renderPagination(total, totalPages) {
    const el = $('#pagination');
    if (!el) return;

    if (totalPages <= 1) {
      el.innerHTML = '';
      return;
    }

    let html = '';
    html += `<button class="btn btn-sm" ${currentPage <= 1 ? 'disabled' : ''} onclick="goToPage(${currentPage - 1})">&laquo; Prev</button>`;
    for (let i = 1; i <= totalPages; i++) {
      html += `<button class="btn btn-sm${i === currentPage ? ' active' : ''}" onclick="goToPage(${i})">${i}</button>`;
    }
    html += `<button class="btn btn-sm" ${currentPage >= totalPages ? 'disabled' : ''} onclick="goToPage(${currentPage + 1})">Next &raquo;</button>`;
    el.innerHTML = html;
  }

  window.goToPage = function(n) {
    currentPage = n;
    filterAndPaginate();
  };

  searchInput.addEventListener('input', () => {
    currentPage = 1;
    filterAndPaginate();
  });

  // Initial sort & render.
  updateHeaders();
  sortRows();
  filterAndPaginate();
})();

// ── Online status polling ───────────────────────────────────────────────────

async function pollOnlineStatus() {
  try {
    const resp = await api.get('/api/users/online');
    const onlineSet = new Set(resp.online || []);
    const sessions = resp.sessions || {};

    $$('[data-uuid]').forEach(el => {
      const uuid = el.dataset.uuid;
      const userName = el.dataset.user;
      if (!uuid) return;
      const badge = el.querySelector('.user-online-badge');
      if (!badge) return;

      const count = userName ? (sessions[userName] || 0) : 0;

      if (onlineSet.has(uuid) || count > 0) {
        badge.textContent = count > 1 ? 'online (' + count + ')' : 'online';
        badge.className = 'badge badge-green user-online-badge';
      } else {
        badge.textContent = 'offline';
        badge.className = 'badge badge-dim user-online-badge';
      }
    });
  } catch (err) {
    // Silently ignore — relay may be unreachable.
  }
}

async function toggleSingleSession(name, enabled) {
  try {
    await api.put('/api/users/' + name + '/single-session', { enabled });
  } catch (err) {
    alert('Error: ' + err.message);
  }
}

// Poll immediately on load and every 15 seconds if there are user rows.
if ($$('[data-uuid]').length > 0) {
  pollOnlineStatus();
  setInterval(pollOnlineStatus, 15000);
}

// ── Per-user bandwidth stats polling ─────────────────────────────────────────

(function() {
  const statsKV = document.getElementById('user-stats-kv');
  if (!statsKV) return;

  const card = statsKV.closest('[data-user]') || document.querySelector('[data-user]');
  const userName = card ? card.dataset.user : null;
  if (!userName) return;

  function setBind(name, text) {
    const el = document.querySelector('[data-bind="' + name + '"]');
    if (el) el.textContent = text;
  }

  async function pollUserStats() {
    try {
      const data = await api.get('/api/stats?user=' + encodeURIComponent(userName));
      if (!data.enabled || !data.snapshots || data.snapshots.length === 0) {
        setBind('user-sent', '—');
        setBind('user-recv', '—');
        setBind('user-active', '0');
        setBind('user-total', '0');
        return;
      }
      const s = data.snapshots[0];
      setBind('user-sent', formatBytes(s.bytes_sent));
      setBind('user-recv', formatBytes(s.bytes_recv));
      setBind('user-active', String(s.active_connections));
      setBind('user-total', String(s.total_connections));
    } catch (_) {}
  }

  setInterval(pollUserStats, 5000);
  pollUserStats();
})();
