package stats

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// TunnelKey identifies a unique tunnel (user + destination port).
type TunnelKey struct {
	User string
	Port int
}

func (k TunnelKey) String() string {
	return fmt.Sprintf("%s:%d", k.User, k.Port)
}

// TunnelStats holds live counters for one tunnel.
type TunnelStats struct {
	BytesSent   atomic.Int64 // user upload (client → server)
	BytesRecv   atomic.Int64 // user download (server → client)
	Connections atomic.Int64 // currently active connections (gauge)
	TotalConns  atomic.Int64 // total connections since start (counter)
}

// Snapshot is a point-in-time read of a TunnelStats.
type Snapshot struct {
	User       string `json:"user"`
	Port       int    `json:"port"`
	BytesSent  int64  `json:"bytes_sent"`
	BytesRecv  int64  `json:"bytes_recv"`
	ActiveConn int64  `json:"active_connections"`
	TotalConn  int64  `json:"total_connections"`
}

// HistoryPoint is a time-stamped set of snapshots (for the ring buffer).
type HistoryPoint struct {
	Time      time.Time  `json:"time"`
	Snapshots []Snapshot `json:"snapshots"`
}

// Collector maintains per-tunnel bandwidth counters.
// Safe for concurrent use from multiple goroutines.
type Collector struct {
	mu      sync.RWMutex
	tunnels map[TunnelKey]*TunnelStats

	histMu   sync.Mutex
	history  []HistoryPoint
	histMax  int
	histDone chan struct{}
}

// New creates a Collector. historySize controls how many snapshots
// to retain in the ring buffer (0 = no history).
func New(historySize int) *Collector {
	c := &Collector{
		tunnels:  make(map[TunnelKey]*TunnelStats),
		history:  make([]HistoryPoint, 0, historySize),
		histMax:  historySize,
		histDone: make(chan struct{}),
	}
	if historySize > 0 {
		go c.historyLoop()
	}
	return c
}

// Get returns (or creates) TunnelStats for the given key.
func (c *Collector) Get(key TunnelKey) *TunnelStats {
	c.mu.RLock()
	ts, ok := c.tunnels[key]
	c.mu.RUnlock()
	if ok {
		return ts
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if ts, ok = c.tunnels[key]; ok {
		return ts
	}
	ts = &TunnelStats{}
	c.tunnels[key] = ts
	return ts
}

// TrackConn increments the active connection counter for a tunnel.
// Returns a function that must be called when the connection closes.
func (c *Collector) TrackConn(key TunnelKey) func() {
	ts := c.Get(key)
	ts.Connections.Add(1)
	ts.TotalConns.Add(1)
	return func() {
		ts.Connections.Add(-1)
	}
}

// Snapshot returns a point-in-time read of all tunnels.
func (c *Collector) Snapshot() []Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]Snapshot, 0, len(c.tunnels))
	for k, ts := range c.tunnels {
		out = append(out, Snapshot{
			User:       k.User,
			Port:       k.Port,
			BytesSent:  ts.BytesSent.Load(),
			BytesRecv:  ts.BytesRecv.Load(),
			ActiveConn: ts.Connections.Load(),
			TotalConn:  ts.TotalConns.Load(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].User != out[j].User {
			return out[i].User < out[j].User
		}
		return out[i].Port < out[j].Port
	})
	return out
}

// UserSnapshot returns an aggregate for a single user (all ports combined).
func (c *Collector) UserSnapshot(user string) Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	s := Snapshot{User: user}
	for k, ts := range c.tunnels {
		if k.User != user {
			continue
		}
		s.BytesSent += ts.BytesSent.Load()
		s.BytesRecv += ts.BytesRecv.Load()
		s.ActiveConn += ts.Connections.Load()
		s.TotalConn += ts.TotalConns.Load()
	}
	return s
}

// History returns the buffered history snapshots.
func (c *Collector) History() []HistoryPoint {
	c.histMu.Lock()
	defer c.histMu.Unlock()
	out := make([]HistoryPoint, len(c.history))
	copy(out, c.history)
	return out
}

// historyLoop periodically snapshots counters into the ring buffer.
func (c *Collector) historyLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.histDone:
			return
		case <-ticker.C:
			snap := c.Snapshot()
			point := HistoryPoint{Time: time.Now(), Snapshots: snap}
			c.histMu.Lock()
			if len(c.history) >= c.histMax {
				c.history = c.history[1:]
			}
			c.history = append(c.history, point)
			c.histMu.Unlock()
		}
	}
}

// Close stops the history goroutine.
func (c *Collector) Close() {
	select {
	case <-c.histDone:
	default:
		close(c.histDone)
	}
}
