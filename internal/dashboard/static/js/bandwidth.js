// ── Bandwidth page — sorting, search, pagination, live polling ───────────────

(function() {
  const PAGE_SIZE = 10;
  let currentPage = 1;
  let sortCol = 'sent';
  let sortDir = 'desc';
  let allRows = []; // cached snapshot data

  const table = $('#bw-table');
  const tbody = $('#bw-body');
  const searchInput = $('#bw-search');
  const badge = document.getElementById('bw-badge');
  if (!table || !tbody || !searchInput) return;

  const headers = table.querySelectorAll('th.sortable');

  // ── Sort helpers ──

  function getSortValue(row) {
    switch (sortCol) {
      case 'user':   return row.user.toLowerCase();
      case 'port':   return row.port;
      case 'sent':   return row.bytes_sent;
      case 'recv':   return row.bytes_recv;
      case 'active': return row.active_connections;
      case 'total':  return row.total_connections;
      default:       return 0;
    }
  }

  function sortRows() {
    allRows.sort((a, b) => {
      const va = getSortValue(a);
      const vb = getSortValue(b);
      let cmp = 0;
      if (typeof va === 'number') {
        cmp = va - vb;
      } else {
        cmp = va.localeCompare(vb);
      }
      return sortDir === 'asc' ? cmp : -cmp;
    });
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
        sortDir = col === 'user' || col === 'port' ? 'asc' : 'desc';
      }
      updateHeaders();
      sortRows();
      currentPage = 1;
      renderTable();
    });
  });

  // ── Filter & paginate ──

  function renderTable() {
    const query = searchInput.value.toLowerCase().trim();

    const matching = query
      ? allRows.filter(r => r.user.toLowerCase().includes(query))
      : allRows;

    const totalPages = Math.max(1, Math.ceil(matching.length / PAGE_SIZE));
    if (currentPage > totalPages) currentPage = totalPages;

    const start = (currentPage - 1) * PAGE_SIZE;
    const page = matching.slice(start, start + PAGE_SIZE);

    if (page.length === 0) {
      tbody.innerHTML = '<tr><td colspan="6" class="text-dim">No data</td></tr>';
    } else {
      let html = '';
      page.forEach(s => {
        html += '<tr>' +
          '<td><a href="/users/' + s.user + '">' + s.user + '</a></td>' +
          '<td class="text-mono">' + s.port + '</td>' +
          '<td class="text-mono">' + formatBytes(s.bytes_sent) + '</td>' +
          '<td class="text-mono">' + formatBytes(s.bytes_recv) + '</td>' +
          '<td class="text-mono">' + s.active_connections + '</td>' +
          '<td class="text-mono">' + s.total_connections + '</td>' +
          '</tr>';
      });
      tbody.innerHTML = html;
    }

    renderPagination(matching.length, totalPages);
  }

  function renderPagination(total, totalPages) {
    const el = $('#bw-pagination');
    if (!el) return;

    if (totalPages <= 1) {
      el.innerHTML = '';
      return;
    }

    let html = '';
    html += '<button class="btn btn-sm" ' + (currentPage <= 1 ? 'disabled' : '') + ' onclick="bwGoToPage(' + (currentPage - 1) + ')">&laquo; Prev</button>';
    for (let i = 1; i <= totalPages; i++) {
      html += '<button class="btn btn-sm' + (i === currentPage ? ' active' : '') + '" onclick="bwGoToPage(' + i + ')">' + i + '</button>';
    }
    html += '<button class="btn btn-sm" ' + (currentPage >= totalPages ? 'disabled' : '') + ' onclick="bwGoToPage(' + (currentPage + 1) + ')">Next &raquo;</button>';
    el.innerHTML = html;
  }

  window.bwGoToPage = function(n) {
    currentPage = n;
    renderTable();
  };

  searchInput.addEventListener('input', () => {
    currentPage = 1;
    renderTable();
  });

  // ── Polling ──

  async function poll() {
    try {
      const data = await api.get('/api/stats');
      if (!data.enabled) {
        tbody.innerHTML = '<tr><td colspan="6" class="text-dim">Analytics disabled</td></tr>';
        if (badge) { badge.textContent = 'disabled'; badge.className = 'badge badge-dim'; }
        return;
      }

      const snaps = data.snapshots || [];
      allRows = snaps;

      let totalActive = 0;
      snaps.forEach(s => { totalActive += s.active_connections; });

      if (badge) {
        badge.textContent = totalActive + ' active';
        badge.className = 'badge ' + (totalActive > 0 ? 'badge-green' : 'badge-dim');
      }

      sortRows();
      renderTable();
    } catch (_) {}
  }

  updateHeaders();
  setInterval(poll, 3000);
  poll();
})();
