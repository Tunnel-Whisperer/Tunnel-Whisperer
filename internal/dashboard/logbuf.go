package dashboard

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// LogEntry is a single log line for the dashboard console.
type LogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"msg"`
}

// logBuffer is a fixed-size ring buffer of log entries with subscriber support.
type logBuffer struct {
	mu      sync.Mutex
	entries []LogEntry
	max     int
	subs    map[int]chan LogEntry
	nextID  int
}

func newLogBuffer(max int) *logBuffer {
	return &logBuffer{
		entries: make([]LogEntry, 0, max),
		max:     max,
		subs:    make(map[int]chan LogEntry),
	}
}

func (b *logBuffer) add(e LogEntry) {
	b.mu.Lock()
	if len(b.entries) >= b.max {
		b.entries = b.entries[1:]
	}
	b.entries = append(b.entries, e)
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default: // drop if subscriber is slow
		}
	}
	b.mu.Unlock()
}

// snapshot returns a copy of all buffered entries.
func (b *logBuffer) snapshot() []LogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]LogEntry, len(b.entries))
	copy(out, b.entries)
	return out
}

// subscribe returns a channel that receives new log entries and an unsubscribe func.
func (b *logBuffer) subscribe() (<-chan LogEntry, func()) {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	ch := make(chan LogEntry, 64)
	b.subs[id] = ch
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		delete(b.subs, id)
		close(ch)
		b.mu.Unlock()
	}
}

// teeHandler is a slog.Handler that forwards records to an inner handler
// and also writes them to a logBuffer for dashboard streaming.
type teeHandler struct {
	inner slog.Handler
	buf   *logBuffer
}

func newTeeHandler(inner slog.Handler, buf *logBuffer) *teeHandler {
	return &teeHandler{inner: inner, buf: buf}
}

func (h *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	msg := r.Message
	// Append key=value attrs.
	r.Attrs(func(a slog.Attr) bool {
		msg += fmt.Sprintf(" %s=%s", a.Key, a.Value.String())
		return true
	})

	h.buf.add(LogEntry{
		Time:    r.Time.Format(time.TimeOnly),
		Level:   r.Level.String(),
		Message: msg,
	})
	return h.inner.Handle(ctx, r)
}

func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &teeHandler{inner: h.inner.WithAttrs(attrs), buf: h.buf}
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	return &teeHandler{inner: h.inner.WithGroup(name), buf: h.buf}
}
