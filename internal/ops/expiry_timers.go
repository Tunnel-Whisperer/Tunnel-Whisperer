package ops

import (
	"log/slog"
	"sync"
	"time"
)

// expiryWatcher disconnects users whose client certificate has expired: one
// timer per user with a future expiry fires at that moment, and a periodic
// rescan picks up users added, re-issued or deleted since, and disconnects any
// user already past expiry. Users without an expiry are never touched.
type expiryWatcher struct {
	o      *Ops
	rescan time.Duration

	mu      sync.Mutex
	timers  map[string]*expiryTimer
	started bool
	stopped bool
	stop    chan struct{}
	done    chan struct{}
}

type expiryTimer struct {
	at    time.Time
	timer *time.Timer
}

func newExpiryWatcher(o *Ops, rescan time.Duration) *expiryWatcher {
	return &expiryWatcher{
		o:      o,
		rescan: rescan,
		timers: map[string]*expiryTimer{},
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
}

// Start rescans once, then every rescan interval until Stop. It does nothing
// once Stop has run.
func (w *expiryWatcher) Start() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.started = true
	w.mu.Unlock()
	w.Rescan()
	go func() {
		defer close(w.done)
		t := time.NewTicker(w.rescan)
		defer t.Stop()
		for {
			select {
			case <-w.stop:
				return
			case <-t.C:
				w.Rescan()
			}
		}
	}()
}

// Stop ends the rescan loop and cancels every pending timer. Later calls are
// no-ops.
func (w *expiryWatcher) Stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	started := w.started
	w.mu.Unlock()
	if started {
		close(w.stop)
		<-w.done
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for name, et := range w.timers {
		et.timer.Stop()
		delete(w.timers, name)
	}
}

// Rescan lists users, (re)arms a timer for each future expiry, drops timers of
// users gone or without an expiry, and disconnects users already expired.
func (w *expiryWatcher) Rescan() {
	users, err := w.o.ListUsers()
	if err != nil {
		slog.Error("expiry rescan: listing users failed", "error", err)
		return
	}

	now := time.Now()
	want := map[string]time.Time{}
	var expired []string
	for _, u := range users {
		if u.ExpiresAt == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, u.ExpiresAt)
		if err != nil {
			slog.Error("expiry rescan: bad expiry", "user", u.Name, "expires_at", u.ExpiresAt, "error", err)
			continue
		}
		if !at.After(now) {
			expired = append(expired, u.Name)
			continue
		}
		want[u.Name] = at
	}

	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	for name, et := range w.timers {
		if at, ok := want[name]; !ok || !at.Equal(et.at) {
			et.timer.Stop()
			delete(w.timers, name)
		}
	}
	for name, at := range want {
		if _, ok := w.timers[name]; ok {
			continue
		}
		et := &expiryTimer{at: at}
		et.timer = time.AfterFunc(time.Until(at), func() { w.fire(name, et) })
		w.timers[name] = et
	}
	w.mu.Unlock()

	for _, name := range expired {
		if n := w.o.srv.DisconnectUser(name); n > 0 {
			slog.Info("user credential expired, sessions disconnected", "user", name, "sessions", n)
		}
	}
}

// fire runs on et's timer. A Forget, re-arm or Stop that raced the timer has
// already removed et from the map, so it does nothing.
func (w *expiryWatcher) fire(name string, et *expiryTimer) {
	w.mu.Lock()
	if w.stopped || w.timers[name] != et {
		w.mu.Unlock()
		return
	}
	delete(w.timers, name)
	w.mu.Unlock()

	n := w.o.srv.DisconnectUser(name)
	slog.Info("user credential expired, sessions disconnected", "user", name, "sessions", n)
}

// Forget drops name's pending timer. It is a no-op on a nil watcher (no
// server running in this process).
func (w *expiryWatcher) Forget(name string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if et, ok := w.timers[name]; ok {
		et.timer.Stop()
		delete(w.timers, name)
	}
}

// pending reports the number of armed timers (tests).
func (w *expiryWatcher) pending() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.timers)
}
