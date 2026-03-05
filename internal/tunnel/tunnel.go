package tunnel

import "log/slog"

// Tunnel represents a single tunnel session between the server and relay.
type Tunnel struct {
	Name       string
	RelayAddr  string
	LocalPort  int
	RemotePort int
	running    bool
}

func New(name, relayAddr string, localPort, remotePort int) *Tunnel {
	return &Tunnel{
		Name:       name,
		RelayAddr:  relayAddr,
		LocalPort:  localPort,
		RemotePort: remotePort,
	}
}

// Start initiates the tunnel.
func (t *Tunnel) Start() error {
	slog.Info("tunnel starting",
		"name", t.Name,
		"relay", t.RelayAddr,
		"local_port", t.LocalPort,
		"remote_port", t.RemotePort,
	)
	t.running = true
	return nil
}

// Stop tears down the tunnel.
func (t *Tunnel) Stop() error {
	slog.Info("tunnel stopping", "name", t.Name)
	t.running = false
	return nil
}

// IsRunning returns whether the tunnel is active.
func (t *Tunnel) IsRunning() bool {
	return t.running
}
