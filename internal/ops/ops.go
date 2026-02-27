package ops

import (
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/logging"
)

// ProgressEvent describes one step in a long-running operation.
type ProgressEvent struct {
	Step    int    `json:"step"`
	Total   int    `json:"total"`
	Label   string `json:"label"`
	Status  string `json:"status"` // "running", "completed", "failed"
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	Data    any    `json:"data,omitempty"`
}

// ProgressFunc is a callback for reporting progress. CLI callers print to
// stdout; dashboard callers forward events to an SSE channel.
type ProgressFunc func(ProgressEvent)

// ServerState represents the lifecycle state of a manager.
type ServerState string

const (
	StateStopped  ServerState = "stopped"
	StateStarting ServerState = "starting"
	StateRunning  ServerState = "running"
	StateStopping ServerState = "stopping"
	StateError    ServerState = "error"
)

// Ops centralises business logic shared by the CLI and the web dashboard.
type Ops struct {
	cfg *config.Config
	mu  sync.Mutex // serialises relay + user operations
	srv serverManager
	cli clientManager

	onlineMu      sync.RWMutex
	onlineCache   map[string]bool
	onlinePoll    time.Time
	onlineRefresh sync.Mutex // prevents concurrent refreshes
	trafficReset  bool       // true after first traffic stats reset
}

// New loads the configuration and returns a ready Ops instance.
func New() (*Ops, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return &Ops{
		cfg: cfg,
		srv: serverManager{state: StateStopped},
		cli: clientManager{state: StateStopped},
	}, nil
}

// Config returns the current configuration (read-only snapshot).
func (o *Ops) Config() *config.Config {
	o.mu.Lock()
	defer o.mu.Unlock()
	c := *o.cfg
	return &c
}

// ReloadConfig re-reads the config file from disk.
func (o *Ops) ReloadConfig() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	o.mu.Lock()
	o.cfg = cfg
	o.mu.Unlock()
	return nil
}

// Mode returns the current operating mode ("server", "client", or "").
func (o *Ops) Mode() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.cfg.Mode
}

// SetMode persists the operating mode to config.
func (o *Ops) SetMode(mode string) error {
	if mode != "server" && mode != "client" {
		return fmt.Errorf("invalid mode: %q (must be \"server\" or \"client\")", mode)
	}
	o.mu.Lock()
	o.cfg.Mode = mode
	cfg := o.cfg
	o.mu.Unlock()
	return config.Save(cfg)
}

// SetProxy validates and persists the proxy URL to config.
// An empty string clears the proxy. Takes effect on next server/client start.
func (o *Ops) SetProxy(proxyURL string) error {
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return fmt.Errorf("invalid proxy URL: %w", err)
		}
		if u.Scheme != "socks5" && u.Scheme != "http" {
			return fmt.Errorf("unsupported proxy scheme %q (use socks5:// or http://)", u.Scheme)
		}
		if u.Hostname() == "" {
			return fmt.Errorf("proxy URL must include a host")
		}
	}
	o.mu.Lock()
	o.cfg.Proxy = proxyURL
	cfg := o.cfg
	o.mu.Unlock()
	return config.Save(cfg)
}

// SetLogLevel validates and persists the log level to config.
// Takes effect on next server/client restart.
func (o *Ops) SetLogLevel(level string) error {
	switch level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid log level: %q (must be debug, info, warn, or error)", level)
	}
	o.mu.Lock()
	o.cfg.LogLevel = level
	cfg := o.cfg
	o.mu.Unlock()
	return config.Save(cfg)
}

// StartServer starts all server components.
func (o *Ops) StartServer(progress ProgressFunc) error {
	return o.srv.Start(o, progress)
}

// StopServer stops all server components.
func (o *Ops) StopServer(progress ProgressFunc) error {
	return o.srv.Stop(progress)
}

// RestartServer stops, waits for the relay to release the port, reloads
// config, and starts again. Progress events are emitted as a single
// continuous SSE stream — Stop's final event is prevented from closing
// the session so Start's events continue through the same connection.
func (o *Ops) RestartServer(progress ProgressFunc) error {
	if progress == nil {
		progress = func(ProgressEvent) {}
	}

	// Wrap Stop's progress so its final "completed" event doesn't trigger
	// SSE terminal detection (both server-side and client-side check
	// Step == Total && Status == "completed").
	stopProgress := func(e ProgressEvent) {
		e.Total++
		progress(e)
	}

	if err := o.srv.Stop(stopProgress); err != nil {
		return err
	}

	// Keep state as "stopping" during the delay so the UI shows a disabled
	// button instead of an active Start button.
	o.srv.mu.Lock()
	o.srv.state = StateStopping
	o.srv.mu.Unlock()

	progress(ProgressEvent{Label: "Waiting for relay to release port", Status: "running"})
	time.Sleep(3 * time.Second)

	o.srv.mu.Lock()
	o.srv.state = StateStopped
	o.srv.mu.Unlock()

	o.ReloadConfig()

	// Apply the new log level from config (affects slog + Xray).
	if cfg := o.Config(); cfg.LogLevel != "" {
		logging.SetLevel(cfg.LogLevel)
	}

	// Start's progress passes through directly — its final event is the
	// true terminal that closes the SSE session.
	return o.srv.Start(o, progress)
}

// ServerStatus returns the server lifecycle state.
func (o *Ops) ServerStatus() ServerStatus {
	return o.srv.Status()
}

// StartClient starts the client connection.
func (o *Ops) StartClient(progress ProgressFunc) error {
	return o.cli.Start(o, progress)
}

// StopClient stops the client connection.
func (o *Ops) StopClient(progress ProgressFunc) error {
	return o.cli.Stop(progress)
}

// ReconnectClient stops, reloads config, and starts the client again.
// Progress events are emitted as a single continuous SSE stream.
func (o *Ops) ReconnectClient(progress ProgressFunc) error {
	if progress == nil {
		progress = func(ProgressEvent) {}
	}

	// Wrap Stop's progress so its final "completed" event doesn't trigger
	// SSE terminal detection.
	stopProgress := func(e ProgressEvent) {
		e.Total++
		progress(e)
	}

	if err := o.cli.Stop(stopProgress); err != nil {
		return err
	}

	o.ReloadConfig()

	// Apply the new log level from config.
	if cfg := o.Config(); cfg.LogLevel != "" {
		logging.SetLevel(cfg.LogLevel)
	}

	return o.cli.Start(o, progress)
}

// ClientStatus returns the client lifecycle state.
func (o *Ops) ClientStatus() ClientStatus {
	return o.cli.Status()
}

// ConfigChanged reports whether the on-disk config differs from the config
// that was active when the running server or client started.
// Returns false if nothing is running.
func (o *Ops) ConfigChanged() bool {
	currentHash := config.FileHash()
	if currentHash == "" {
		return false
	}

	o.srv.mu.Lock()
	srvHash := o.srv.cfgHash
	srvState := o.srv.state
	o.srv.mu.Unlock()
	slog.Debug("config change check", "disk_hash", currentHash[:12], "srv_hash", srvHash[:min(12, len(srvHash))], "srv_state", srvState)
	if srvState == StateRunning && srvHash != "" && srvHash != currentHash {
		return true
	}

	o.cli.mu.Lock()
	cliHash := o.cli.cfgHash
	cliState := o.cli.state
	o.cli.mu.Unlock()
	if cliState == StateRunning && cliHash != "" && cliHash != currentHash {
		return true
	}

	return false
}
