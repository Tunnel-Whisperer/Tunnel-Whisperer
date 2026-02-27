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
      el.innerHTML = `<span class="step-num">[${event.step}/${event.total}]</span><span class="step-label"></span><span class="step-msg"></span>`;
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

// ── Utility ─────────────────────────────────────────────────────────────────

function $(sel, ctx) { return (ctx || document).querySelector(sel); }
function $$(sel, ctx) { return [...(ctx || document).querySelectorAll(sel)]; }
