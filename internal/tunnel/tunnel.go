package tunnel

import "log"

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
	log.Printf("tunnel[%s]: starting %s local:%d -> remote:%d", t.Name, t.RelayAddr, t.LocalPort, t.RemotePort)
	t.running = true
	return nil
}

// Stop tears down the tunnel.
func (t *Tunnel) Stop() error {
	log.Printf("tunnel[%s]: stopping", t.Name)
	t.running = false
	return nil
}

// IsRunning returns whether the tunnel is active.
func (t *Tunnel) IsRunning() bool {
	return t.running
}
