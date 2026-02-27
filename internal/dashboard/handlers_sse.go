package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

// sseHub manages SSE sessions for long-running operations.
type sseHub struct {
	mu       sync.Mutex
	sessions map[string]*sseSession
}

type sseSession struct {
	ch   chan ops.ProgressEvent
	done chan struct{}
}

func newSSEHub() *sseHub {
	return &sseHub{sessions: make(map[string]*sseSession)}
}

// create returns a new session ID and a ProgressFunc that writes to the session channel.
func (h *sseHub) create() (string, ops.ProgressFunc) {
	id := uuid.New().String()[:8]
	sess := &sseSession{
		ch:   make(chan ops.ProgressEvent, 64),
		done: make(chan struct{}),
	}

	h.mu.Lock()
	h.sessions[id] = sess
	h.mu.Unlock()

	progress := func(e ops.ProgressEvent) {
		select {
		case sess.ch <- e:
		default:
			// Drop if buffer full.
		}

		// If this is a terminal event, close the channel.
		if e.Status == "failed" || (e.Status == "completed" && e.Step == e.Total) {
			select {
			case <-sess.done:
			default:
				close(sess.done)
			}
		}
	}

	return id, progress
}

// get retrieves a session by ID.
func (h *sseHub) get(id string) *sseSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessions[id]
}

// remove cleans up a session.
func (h *sseHub) remove(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sessions, id)
}

// apiEvents streams SSE events for a session.
func (s *Server) apiEvents(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/events/")
	if id == "" {
		http.Error(w, "session ID required", http.StatusBadRequest)
		return
	}

	sess := s.sse.get(id)
	if sess == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			s.sse.remove(id)
			return
		case event, ok := <-sess.ch:
			if !ok {
				s.sse.remove(id)
				return
			}
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

			// If terminal, drain remaining and close.
			if event.Status == "failed" || (event.Status == "completed" && event.Step == event.Total) {
				// Drain any remaining buffered events.
			drainLoop:
				for {
					select {
					case extra, ok := <-sess.ch:
						if !ok {
							break drainLoop
						}
						data, _ := json.Marshal(extra)
						fmt.Fprintf(w, "data: %s\n\n", data)
						flusher.Flush()
					default:
						break drainLoop
					}
				}
				s.sse.remove(id)
				return
			}
		case <-sess.done:
			// Drain remaining.
		drainDone:
			for {
				select {
				case extra, ok := <-sess.ch:
					if !ok {
						break drainDone
					}
					data, _ := json.Marshal(extra)
					fmt.Fprintf(w, "data: %s\n\n", data)
					flusher.Flush()
				default:
					break drainDone
				}
			}
			s.sse.remove(id)
			return
		}
	}
}
