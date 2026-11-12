// ── Fetch wrapper ────────────────────────────────────────────────────────────

const api = {
  async get(url) {
    const resp = await fetch(url);
    if (!resp.ok) throw new Error(`GET ${url}: ${resp.status}`);
    return resp.json();
  },

  async post(url, body) {
    const resp = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (!resp.ok) {
      const text = await resp.text();
      throw new Error(text || `POST ${url}: ${resp.status}`);
    }
    return resp.json();
  },

  async put(url, body) {
    const resp = await fetch(url, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (!resp.ok) {
      const text = await resp.text();
      throw new Error(text || `PUT ${url}: ${resp.status}`);
    }
    return resp.json();
  },

  async del(url) {
    const resp = await fetch(url, { method: 'DELETE' });
    if (!resp.ok) {
      const text = await resp.text();
      throw new Error(text || `DELETE ${url}: ${resp.status}`);
    }
    return resp.json();
  },
};

// ── SSE helper ──────────────────────────────────────────────────────────────

function connectSSE(sessionID, onEvent, onDone) {
  const source = new EventSource(`/api/events/${sessionID}`);
  source.onmessage = (e) => {
    const event = JSON.parse(e.data);
    onEvent(event);
    if (event.status === 'completed' && event.step === event.total) {
      source.close();
      if (onDone) onDone(null);
    }
    if (event.status === 'failed') {
      source.close();
      if (onDone) onDone(new Error(event.error || event.message));
    }
  };
  source.onerror = () => {
    source.close();
    if (onDone) onDone(null); // stream ended
  };
  return source;
}

// ── Progress log renderer ───────────────────────────────────────────────────

function renderProgressEvent(container, event) {
  // Structured step events (step > 0 with total) update in-place per step.
  // Streaming events (step === 0 or no total) append as log lines.
  if (event.step > 0 && event.total > 0) {
    let el = container.querySelector(`[data-step="${event.step}"]`);
    if (!el) {
      el = document.createElement('div');
      el.className = 'progress-step';
      el.dataset.step = event.step;
      el.append(
        makeEl('span', 'step-num', `[${event.step}/${event.total}]`),
        makeEl('span', 'step-label'),
        makeEl('span', 'step-msg'),
      );
      container.appendChild(el);
    }
    el.className = `progress-step ${event.status}`;
    el.querySelector('.step-label').textContent = event.label;
    el.querySelector('.step-msg').textContent = event.message || event.error || '';
  } else {
    // Streaming log line — always append.
    const el = document.createElement('div');
    el.className = 'progress-line';
    el.textContent = event.message || event.label || event.error || '';
    container.appendChild(el);
  }

  // Auto-scroll.
  container.scrollTop = container.scrollHeight;
}

// ── Safe DOM builders ───────────────────────────────────────────────────────
// Server, error, and user-derived strings must only reach the DOM as text.

function makeEl(tag, className, text) {
  const el = document.createElement(tag);
  if (className) el.className = className;
  if (text !== undefined && text !== null) el.textContent = String(text);
  return el;
}

function appendFailedStep(container, text) {
  const step = makeEl('div', 'progress-step failed');
  step.appendChild(makeEl('span', 'step-label', text));
  container.appendChild(step);
}

function showAlert(container, text, isError = true) {
  container.replaceChildren(makeEl('div', isError ? 'alert alert-error' : 'alert', text));
}

function renderPager(el, currentPage, totalPages, goTo) {
  const btn = (label, page, disabled, active) => {
    const b = makeEl('button', 'btn btn-sm' + (active ? ' active' : ''), label);
    b.disabled = disabled;
    b.addEventListener('click', () => goTo(page));
    return b;
  };
  el.replaceChildren(btn('« Prev', currentPage - 1, currentPage <= 1, false));
  for (let i = 1; i <= totalPages; i++) {
    el.appendChild(btn(String(i), i, false, i === currentPage));
  }
  el.appendChild(btn('Next »', currentPage + 1, currentPage >= totalPages, false));
}

// ── Utility ─────────────────────────────────────────────────────────────────

function $(sel, ctx) { return (ctx || document).querySelector(sel); }
function $$(sel, ctx) { return [...(ctx || document).querySelectorAll(sel)]; }

function formatBytes(bytes) {
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const val = bytes / Math.pow(1024, i);
  return (i === 0 ? val : val.toFixed(1)) + ' ' + units[i];
}
