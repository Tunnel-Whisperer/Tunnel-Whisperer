package ops

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"github.com/tunnelwhisperer/tw/internal/config"
	twssh "github.com/tunnelwhisperer/tw/internal/ssh"
	twxray "github.com/tunnelwhisperer/tw/internal/xray"
)

// ServerStatus describes the server lifecycle state.
type ServerStatus struct {
	State       ServerState `json:"state"`
	SSH         bool        `json:"ssh"`
	Xray        bool        `json:"xray"`
	Tunnel      bool        `json:"tunnel"`
	Error       string      `json:"error,omitempty"`
	TunnelError string      `json:"tunnel_error,omitempty"`
}

// serverManager controls the lifecycle of all server components.
type serverManager struct {
	mu       sync.Mutex
	state    ServerState
	lastErr  string
	cfgHash  string // config hash at startup, for change detection
	sshSrv   *twssh.Server
	xrayInst *twxray.Instance
	tunnel   *twssh.ReverseTunnel
}

// Start launches all server components (SSH, Xray, reverse tunnel).
func (m *serverManager) Start(o *Ops, progress ProgressFunc) error {
	m.mu.Lock()
	if m.state == StateRunning || m.state == StateStarting {
		m.mu.Unlock()
		return fmt.Errorf("server already %s", m.state)
	}
	m.state = StateStarting
	m.lastErr = ""
	m.mu.Unlock()

	if progress == nil {
		progress = func(ProgressEvent) {}
	}

	cfg := o.Config()

	m.mu.Lock()
	m.cfgHash = config.FileHash()
	m.mu.Unlock()

	fail := func(step, total int, label string, err error) error {
		m.mu.Lock()
		m.state = StateError
		m.lastErr = err.Error()
		m.mu.Unlock()
		progress(ProgressEvent{Step: step, Total: total, Label: label, Status: "failed", Error: err.Error()})
		return err
	}

	total := 2
	if cfg.Xray.RelayHost != "" {
		total = 4
	}

	// Step 1: Ensure keys.
	progress(ProgressEvent{Step: 1, Total: total, Label: "SSH keys", Status: "running"})
	if err := o.EnsureKeys(); err != nil {
		return fail(1, total, "SSH keys", err)
	}
	progress(ProgressEvent{Step: 1, Total: total, Label: "SSH keys", Status: "completed"})

	// Step 2: Start SSH server.
	progress(ProgressEvent{Step: 2, Total: total, Label: "SSH server", Status: "running"})
	sshServer, err := twssh.NewServer(cfg.Server.SSHPort, config.HostKeyDir(), config.AuthorizedKeysPath())
	if err != nil {
		return fail(2, total, "SSH server", err)
	}
	sshServer.OnConnect = func(user string) {
		slog.Info("client connected, refreshing online status", "user", user)
		o.InvalidateOnlineCache()
	}
	sshServer.OnDisconnect = func(user string) {
		slog.Info("client disconnected, refreshing online status", "user", user)
		o.InvalidateOnlineCache()
	}
	go func() {
		if err := sshServer.Run(); err != nil {
			slog.Error("SSH server error", "error", err)
		}
	}()
	m.mu.Lock()
	m.sshSrv = sshServer
	m.mu.Unlock()
	progress(ProgressEvent{Step: 2, Total: total, Label: "SSH server", Status: "completed", Message: fmt.Sprintf("listening on :%d", cfg.Server.SSHPort)})

	step := 3

	// Steps 3-4: Xray + reverse tunnel (if relay configured).
	if cfg.Xray.RelayHost != "" {
		if cfg.Xray.UUID == "" {
			cfg.Xray.UUID = uuid.New().String()
			if err := config.Save(cfg); err != nil {
				slog.Warn("could not save generated UUID", "error", err)
			}
		}

		progress(ProgressEvent{Step: step, Total: total, Label: "Xray tunnel", Status: "running"})
		xrayInstance, err := twxray.New(cfg.Xray)
		if err != nil {
			return fail(step, total, "Xray tunnel", err)
		}
		if err := xrayInstance.Start(cfg.Server.SSHPort, cfg.Server.RelaySSHPort, cfg.Proxy); err != nil {
			return fail(step, total, "Xray tunnel", err)
		}
		m.mu.Lock()
		m.xrayInst = xrayInstance
		m.mu.Unlock()
		progress(ProgressEvent{Step: step, Total: total, Label: "Xray tunnel", Status: "completed", Message: fmt.Sprintf("%s:%d%s", cfg.Xray.RelayHost, cfg.Xray.RelayPort, cfg.Xray.Path)})

		step++
		xrayListenPort := cfg.Server.SSHPort + 1
		progress(ProgressEvent{Step: step, Total: total, Label: "Reverse tunnel", Status: "running"})
		privPath := filepath.Join(config.Dir(), "id_ed25519")
		rt := &twssh.ReverseTunnel{
			RemoteAddr: fmt.Sprintf("127.0.0.1:%d", xrayListenPort),
			User:       cfg.Server.RelaySSHUser,
			KeyPath:    privPath,
			RemotePort: cfg.Server.RemotePort,
			LocalAddr:  fmt.Sprintf("127.0.0.1:%d", cfg.Server.SSHPort),
		}
		go func() {
			if err := rt.Run(); err != nil {
				slog.Error("reverse tunnel error", "error", err)
			}
		}()
		m.mu.Lock()
		m.tunnel = rt
		m.mu.Unlock()
		progress(ProgressEvent{Step: step, Total: total, Label: "Reverse tunnel", Status: "completed", Message: fmt.Sprintf("relay :%d → local :%d", cfg.Server.RemotePort, cfg.Server.SSHPort)})
	}

	m.mu.Lock()
	m.state = StateRunning
	m.mu.Unlock()

	// Patch relay stats config in the background if needed.
	go o.EnsureRelayStats()

	return nil
}

// Stop shuts down all server components in reverse order.
func (m *serverManager) Stop(progress ProgressFunc) error {
	m.mu.Lock()
	if m.state != StateRunning && m.state != StateError {
		m.mu.Unlock()
		return fmt.Errorf("server not running (state: %s)", m.state)
	}
	m.state = StateStopping
	m.mu.Unlock()

	if progress == nil {
		progress = func(ProgressEvent) {}
	}

	step := 1
	total := 0
	m.mu.Lock()
	if m.tunnel != nil {
		total++
	}
	if m.xrayInst != nil {
		total++
	}
	if m.sshSrv != nil {
		total++
	}
	m.mu.Unlock()

	if total == 0 {
		total = 1
	}

	m.mu.Lock()
	if m.tunnel != nil {
		m.mu.Unlock()
		progress(ProgressEvent{Step: step, Total: total, Label: "Reverse tunnel", Status: "running"})
		m.tunnel.Stop()
		m.mu.Lock()
		m.tunnel = nil
		m.mu.Unlock()
		progress(ProgressEvent{Step: step, Total: total, Label: "Reverse tunnel", Status: "completed"})
		step++
		m.mu.Lock()
	}

	if m.xrayInst != nil {
		m.mu.Unlock()
		progress(ProgressEvent{Step: step, Total: total, Label: "Xray tunnel", Status: "running"})
		m.xrayInst.Close()
		m.mu.Lock()
		m.xrayInst = nil
		m.mu.Unlock()
		progress(ProgressEvent{Step: step, Total: total, Label: "Xray tunnel", Status: "completed"})
		step++
		m.mu.Lock()
	}

	if m.sshSrv != nil {
		m.mu.Unlock()
		progress(ProgressEvent{Step: step, Total: total, Label: "SSH server", Status: "running"})
		m.sshSrv.Stop()
		m.mu.Lock()
		m.sshSrv = nil
		m.mu.Unlock()
		progress(ProgressEvent{Step: step, Total: total, Label: "SSH server", Status: "completed"})
		m.mu.Lock()
	}
	m.mu.Unlock()

	m.mu.Lock()
	m.state = StateStopped
	m.lastErr = ""
	m.mu.Unlock()

	return nil
}

// Status returns the current server state with real health checks.
func (m *serverManager) Status() ServerStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := ServerStatus{
		State: m.state,
		SSH:   m.sshSrv != nil,
		Error: m.lastErr,
	}

	// Xray: check if the instance is actually running, not just allocated.
	if m.xrayInst != nil {
		s.Xray = m.xrayInst.Running()
	}

	// Tunnel: check real connection state, not just pointer existence.
	if m.tunnel != nil {
		s.Tunnel = m.tunnel.Connected()
		s.TunnelError = m.tunnel.LastError()
	}

	return s
}
