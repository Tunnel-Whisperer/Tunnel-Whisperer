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

// ClientStatus describes the client lifecycle state.
type ClientStatus struct {
	State       ServerState `json:"state"`
	Xray        bool        `json:"xray"`
	Tunnel      bool        `json:"tunnel"`
	Error       string      `json:"error,omitempty"`
	TunnelError string      `json:"tunnel_error,omitempty"`
}

// clientManager controls the lifecycle of client components.
type clientManager struct {
	mu       sync.Mutex
	state    ServerState
	lastErr  string
	cfgHash  string // config hash at startup, for change detection
	xrayInst *twxray.Instance
	tunnel   *twssh.ForwardTunnel
}

// Start launches the client connection (Xray client + forward tunnel).
func (m *clientManager) Start(o *Ops, progress ProgressFunc) error {
	m.mu.Lock()
	if m.state == StateRunning || m.state == StateStarting {
		m.mu.Unlock()
		return fmt.Errorf("client already %s", m.state)
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

	fail := func(step int, label string, err error) error {
		m.mu.Lock()
		m.state = StateError
		m.lastErr = err.Error()
		m.mu.Unlock()
		progress(ProgressEvent{Step: step, Total: 3, Label: label, Status: "failed", Error: err.Error()})
		return err
	}

	// Validate config.
	if cfg.Xray.RelayHost == "" {
		return fail(1, "Config validation", fmt.Errorf("xray.relay_host must be set"))
	}
	if len(cfg.Client.Tunnels) == 0 {
		return fail(1, "Config validation", fmt.Errorf("no tunnels defined in client.tunnels"))
	}

	// Auto-generate UUID if missing.
	if cfg.Xray.UUID == "" {
		cfg.Xray.UUID = uuid.New().String()
		if err := config.Save(cfg); err != nil {
			slog.Warn("could not save generated UUID", "error", err)
		}
	}

	// Step 1: Ensure keys.
	progress(ProgressEvent{Step: 1, Total: 3, Label: "SSH keys", Status: "running"})
	if err := o.EnsureKeys(); err != nil {
		return fail(1, "SSH keys", err)
	}
	progress(ProgressEvent{Step: 1, Total: 3, Label: "SSH keys", Status: "completed"})

	// Step 2: Start Xray client.
	progress(ProgressEvent{Step: 2, Total: 3, Label: "Xray tunnel", Status: "running"})
	xrayInstance, err := twxray.NewClient(cfg.Xray)
	if err != nil {
		return fail(2, "Xray tunnel", err)
	}
	if err := xrayInstance.StartClient(cfg.Client, cfg.Proxy); err != nil {
		return fail(2, "Xray tunnel", err)
	}
	m.mu.Lock()
	m.xrayInst = xrayInstance
	m.mu.Unlock()
	progress(ProgressEvent{Step: 2, Total: 3, Label: "Xray tunnel", Status: "completed", Message: fmt.Sprintf("%s:%d%s", cfg.Xray.RelayHost, cfg.Xray.RelayPort, cfg.Xray.Path)})

	// Step 3: Start forward tunnel.
	progress(ProgressEvent{Step: 3, Total: 3, Label: "Port forwarding", Status: "running"})
	mappings := make([]twssh.Mapping, len(cfg.Client.Tunnels))
	for i, t := range cfg.Client.Tunnels {
		mappings[i] = twssh.Mapping{
			LocalPort:  t.LocalPort,
			RemoteHost: t.RemoteHost,
			RemotePort: t.RemotePort,
		}
	}

	privPath := filepath.Join(config.Dir(), "id_ed25519")
	ft := &twssh.ForwardTunnel{
		RemoteAddr: fmt.Sprintf("127.0.0.1:%d", twxray.ClientListenPort),
		User:       cfg.Client.SSHUser,
		KeyPath:    privPath,
		Mappings:   mappings,
	}
	go func() {
		if err := ft.Run(); err != nil {
			slog.Error("forward tunnel error", "error", err)
		}
	}()
	m.mu.Lock()
	m.tunnel = ft
	m.mu.Unlock()

	var desc []string
	for _, t := range cfg.Client.Tunnels {
		desc = append(desc, fmt.Sprintf("localhost:%d → %s:%d", t.LocalPort, t.RemoteHost, t.RemotePort))
	}
	progress(ProgressEvent{Step: 3, Total: 3, Label: "Port forwarding", Status: "completed", Message: fmt.Sprintf("%d tunnel(s) active", len(mappings))})

	m.mu.Lock()
	m.state = StateRunning
	m.mu.Unlock()

	return nil
}

// Stop shuts down the client connection.
func (m *clientManager) Stop(progress ProgressFunc) error {
	m.mu.Lock()
	if m.state != StateRunning && m.state != StateError {
		m.mu.Unlock()
		return fmt.Errorf("client not running (state: %s)", m.state)
	}
	m.state = StateStopping
	m.mu.Unlock()

	if progress == nil {
		progress = func(ProgressEvent) {}
	}

	progress(ProgressEvent{Step: 1, Total: 2, Label: "Port forwarding", Status: "running"})
	m.mu.Lock()
	if m.tunnel != nil {
		m.mu.Unlock()
		m.tunnel.Stop()
		m.mu.Lock()
		m.tunnel = nil
	}
	m.mu.Unlock()
	progress(ProgressEvent{Step: 1, Total: 2, Label: "Port forwarding", Status: "completed"})

	progress(ProgressEvent{Step: 2, Total: 2, Label: "Xray tunnel", Status: "running"})
	m.mu.Lock()
	if m.xrayInst != nil {
		m.mu.Unlock()
		m.xrayInst.Close()
		m.mu.Lock()
		m.xrayInst = nil
	}
	m.mu.Unlock()
	progress(ProgressEvent{Step: 2, Total: 2, Label: "Xray tunnel", Status: "completed"})

	m.mu.Lock()
	m.state = StateStopped
	m.lastErr = ""
	m.mu.Unlock()

	return nil
}

// Status returns the current client state with real health checks.
func (m *clientManager) Status() ClientStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := ClientStatus{
		State: m.state,
		Error: m.lastErr,
	}

	if m.xrayInst != nil {
		s.Xray = m.xrayInst.Running()
	}

	if m.tunnel != nil {
		s.Tunnel = m.tunnel.Connected()
		s.TunnelError = m.tunnel.LastError()
	}

	return s
}
