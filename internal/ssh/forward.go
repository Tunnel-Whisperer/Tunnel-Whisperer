package ssh

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// Mapping defines a single local-port → remote-host:port forwarding rule.
type Mapping struct {
	LocalPort  int
	RemoteHost string
	RemotePort int
}

// ForwardTunnel connects to a remote SSH server and sets up multiple local
// port forwards (-L) over a single SSH session.
type ForwardTunnel struct {
	// Remote SSH server to connect to (via Xray tunnel).
	RemoteAddr string
	// SSH user on the remote server.
	User string
	// Path to the private key for authentication.
	KeyPath string
	// Port mappings to forward.
	Mappings []Mapping

	mu        sync.Mutex
	client    *gossh.Client
	listeners []net.Listener
	done      chan struct{}
	connected bool
	lastErr   string
}

// Connected reports whether the tunnel currently has an active SSH connection.
func (ft *ForwardTunnel) Connected() bool {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return ft.connected
}

// LastError returns the most recent connection error, or "" if connected.
func (ft *ForwardTunnel) LastError() string {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return ft.lastErr
}

// Run connects to the remote SSH server, starts all local listeners, and
// forwards connections through SSH. It automatically reconnects with
// exponential backoff on failure.
func (ft *ForwardTunnel) Run() error {
	ft.done = make(chan struct{})
	backoff := time.Second * 2
	attempt := 0

	for {
		select {
		case <-ft.done:
			return nil
		default:
		}

		err := ft.connect()
		if err != nil {
			slog.Warn("forward tunnel connection failed", "error", err)
			ft.mu.Lock()
			ft.connected = false
			ft.lastErr = err.Error()
			ft.mu.Unlock()
			attempt++
		} else {
			backoff = time.Second * 2
			attempt = 0
		}

		// Clean up before reconnecting.
		ft.cleanup()

		select {
		case <-ft.done:
			return nil
		case <-time.After(backoff):
			slog.Info("forward tunnel reconnecting", "backoff", backoff, "attempt", attempt)
		}

		// Gradual backoff: stay at each level for 4 attempts before escalating.
		// 2s ×8, 4s ×4, 8s ×4, 16s ×4, then 30s forever.
		if attempt >= 8 && backoff == 2*time.Second {
			backoff = 4 * time.Second
		} else if attempt >= 12 && backoff == 4*time.Second {
			backoff = 8 * time.Second
		} else if attempt >= 16 && backoff == 8*time.Second {
			backoff = 16 * time.Second
		} else if attempt >= 20 && backoff == 16*time.Second {
			backoff = 30 * time.Second
		}
	}
}

// cleanup closes all listeners and the SSH client so ports are freed
// for the next reconnection attempt.
func (ft *ForwardTunnel) cleanup() {
	ft.mu.Lock()
	defer ft.mu.Unlock()

	for _, l := range ft.listeners {
		l.Close()
	}
	ft.listeners = nil

	if ft.client != nil {
		ft.client.Close()
		ft.client = nil
	}

	ft.connected = false
}

func (ft *ForwardTunnel) connect() error {
	keyData, err := os.ReadFile(ft.KeyPath)
	if err != nil {
		return fmt.Errorf("reading private key: %w", err)
	}

	signer, err := gossh.ParsePrivateKey(keyData)
	if err != nil {
		return fmt.Errorf("parsing private key: %w", err)
	}

	sshConfig := &gossh.ClientConfig{
		User: ft.User,
		Auth: []gossh.AuthMethod{
			gossh.PublicKeys(signer),
		},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	slog.Debug("forward tunnel connecting", "remote", ft.RemoteAddr, "user", ft.User)

	conn, err := net.DialTimeout("tcp", ft.RemoteAddr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("dialing %s: %w", ft.RemoteAddr, err)
	}

	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(30 * time.Second)
	}

	sshConn, chans, reqs, err := gossh.NewClientConn(conn, ft.RemoteAddr, sshConfig)
	if err != nil {
		conn.Close()
		return fmt.Errorf("SSH handshake: %w", err)
	}

	ft.mu.Lock()
	ft.client = gossh.NewClient(sshConn, chans, reqs)
	ft.mu.Unlock()

	// Start SSH keepalive — on failure it closes all listeners and the SSH
	// connection so connect() returns and the reconnect loop fires.
	go ft.keepalive(sshConn)

	// Start a local listener for each mapping.
	// All listeners share the same SSH client.
	acceptDone := make(chan struct{})
	var wg sync.WaitGroup

	for _, m := range ft.Mappings {
		listenAddr := fmt.Sprintf("127.0.0.1:%d", m.LocalPort)
		listener, err := net.Listen("tcp", listenAddr)
		if err != nil {
			close(acceptDone)
			wg.Wait()
			ft.client.Close()
			return fmt.Errorf("listening on %s: %w", listenAddr, err)
		}

		ft.mu.Lock()
		ft.listeners = append(ft.listeners, listener)
		ft.mu.Unlock()

		slog.Info("forward tunnel active", "local_port", m.LocalPort, "remote", fmt.Sprintf("%s:%d", m.RemoteHost, m.RemotePort))

		wg.Add(1)
		go func(l net.Listener, m Mapping) {
			defer wg.Done()
			ft.acceptLoop(l, m, acceptDone)
		}(listener, m)
	}

	ft.mu.Lock()
	ft.connected = true
	ft.lastErr = ""
	ft.mu.Unlock()

	// Block until all accept loops finish (triggered by keepalive failure or Stop).
	wg.Wait()

	select {
	case <-ft.done:
		return nil
	default:
		return fmt.Errorf("all listeners closed")
	}
}

// acceptLoop accepts connections on a listener and forwards them through SSH.
func (ft *ForwardTunnel) acceptLoop(listener net.Listener, m Mapping, done <-chan struct{}) {
	for {
		local, err := listener.Accept()
		if err != nil {
			select {
			case <-ft.done:
			case <-done:
			default:
				slog.Warn("forward tunnel accept error", "port", m.LocalPort, "error", err)
			}
			return
		}

		go ft.forward(local, m)
	}
}

// keepalive sends periodic SSH keepalive requests to detect dead connections.
// On failure, it closes all listeners and the SSH connection so that
// connect() unblocks and the reconnect loop fires.
func (ft *ForwardTunnel) keepalive(conn gossh.Conn) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ft.done:
			return
		case <-ticker.C:
			_, _, err := conn.SendRequest("keepalive@tw", true, nil)
			if err != nil {
				slog.Warn("forward tunnel keepalive failed, triggering reconnect", "error", err)
				// Close listeners first — this unblocks Accept() in all loops.
				ft.mu.Lock()
				for _, l := range ft.listeners {
					l.Close()
				}
				ft.mu.Unlock()
				conn.Close()
				return
			}
		}
	}
}

func (ft *ForwardTunnel) forward(local net.Conn, m Mapping) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic in forward tunnel", "error", r)
		}
	}()
	defer local.Close()

	ft.mu.Lock()
	client := ft.client
	ft.mu.Unlock()

	if client == nil {
		slog.Error("forward tunnel has no SSH client")
		return
	}

	remoteAddr := fmt.Sprintf("%s:%d", m.RemoteHost, m.RemotePort)
	remote, err := client.Dial("tcp", remoteAddr)
	if err != nil {
		slog.Error("forward tunnel dial failed", "remote", remoteAddr, "error", err)
		return
	}
	defer remote.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(remote, local)
		if tc, ok := remote.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	go func() {
		defer wg.Done()
		io.Copy(local, remote)
		if tc, ok := local.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	wg.Wait()
}

// Stop shuts down the forward tunnel.
func (ft *ForwardTunnel) Stop() {
	if ft.done != nil {
		close(ft.done)
	}
	ft.cleanup()
}
