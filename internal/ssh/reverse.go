package ssh

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// ReverseTunnel connects to a remote SSH server and sets up a reverse
// port forward (-R) so that remote clients can reach a local port.
type ReverseTunnel struct {
	// Remote SSH server to connect to (via Xray tunnel).
	RemoteAddr string
	// SSH user on the remote server.
	User string
	// Path to the private key for authentication.
	KeyPath string
	// Port on the remote server to listen on.
	RemotePort int
	// Local address to forward to (e.g. "127.0.0.1:2222").
	LocalAddr string

	mu            sync.Mutex
	client        *gossh.Client
	listener      net.Listener
	done          chan struct{}
	keepaliveStop chan struct{}
	connected     bool
	lastErr       string
}

// Connected reports whether the tunnel currently has an active SSH connection.
func (rt *ReverseTunnel) Connected() bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.connected
}

// LastError returns the most recent connection error, or "" if connected.
func (rt *ReverseTunnel) LastError() string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.lastErr
}

// Run connects to the remote SSH server, sets up the reverse port
// forward, and blocks until the tunnel is closed or an error occurs.
// It automatically reconnects with exponential backoff on failure.
func (rt *ReverseTunnel) Run() error {
	rt.done = make(chan struct{})
	backoff := time.Second * 2
	attempt := 0

	for {
		select {
		case <-rt.done:
			return nil
		default:
		}

		err := rt.connect()
		if err != nil {
			slog.Warn("reverse tunnel connection failed", "error", err)
			rt.mu.Lock()
			rt.connected = false
			rt.lastErr = err.Error()
			rt.mu.Unlock()

			// If the session was established before it dropped, reset
			// backoff so we reconnect quickly instead of waiting 16-30s.
			var ce *connectedError
			if errors.As(err, &ce) {
				backoff = time.Second * 2
				attempt = 0
			} else {
				attempt++
			}
		} else {
			// Successful connection resets backoff.
			backoff = time.Second * 2
			attempt = 0
		}

		// Clean up before reconnecting.
		rt.cleanup()

		select {
		case <-rt.done:
			return nil
		case <-time.After(backoff):
			slog.Info("reverse tunnel reconnecting", "backoff", backoff, "attempt", attempt)
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

// cleanup stops the keepalive goroutine, closes the listener and SSH client
// so the next reconnect starts fresh.
func (rt *ReverseTunnel) cleanup() {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if rt.keepaliveStop != nil {
		select {
		case <-rt.keepaliveStop:
		default:
			close(rt.keepaliveStop)
		}
		rt.keepaliveStop = nil
	}

	if rt.listener != nil {
		rt.listener.Close()
		rt.listener = nil
	}
	if rt.client != nil {
		rt.client.Close()
		rt.client = nil
	}
	rt.connected = false
}

func (rt *ReverseTunnel) connect() error {
	keyData, err := os.ReadFile(rt.KeyPath)
	if err != nil {
		return fmt.Errorf("reading private key: %w", err)
	}

	signer, err := gossh.ParsePrivateKey(keyData)
	if err != nil {
		return fmt.Errorf("parsing private key: %w", err)
	}

	config := &gossh.ClientConfig{
		User: rt.User,
		Auth: []gossh.AuthMethod{
			gossh.PublicKeys(signer),
		},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	slog.Debug("reverse tunnel connecting", "remote", rt.RemoteAddr, "user", rt.User)

	conn, err := net.DialTimeout("tcp", rt.RemoteAddr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("dialing %s: %w", rt.RemoteAddr, err)
	}

	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(30 * time.Second)
	}

	sshConn, chans, reqs, err := gossh.NewClientConn(conn, rt.RemoteAddr, config)
	if err != nil {
		conn.Close()
		return fmt.Errorf("SSH handshake: %w", err)
	}

	kaStop := make(chan struct{})

	rt.mu.Lock()
	rt.client = gossh.NewClient(sshConn, chans, reqs)
	rt.keepaliveStop = kaStop
	rt.mu.Unlock()

	// Start SSH keepalive in background.
	go rt.keepalive(sshConn, kaStop)

	// Request reverse port forward.
	listener, err := rt.client.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", rt.RemotePort))
	if err != nil {
		rt.client.Close()
		return fmt.Errorf("requesting reverse forward on :%d: %w", rt.RemotePort, err)
	}

	rt.mu.Lock()
	rt.listener = listener
	rt.connected = true
	rt.lastErr = ""
	rt.mu.Unlock()

	slog.Info("reverse tunnel active", "relay_port", rt.RemotePort, "local", rt.LocalAddr)

	for {
		remote, err := listener.Accept()
		if err != nil {
			select {
			case <-rt.done:
				return nil
			default:
			}
			// Return a connectedError so Run() knows the session was
			// established before it failed (backoff should reset).
			return &connectedError{err: fmt.Errorf("accepting reverse connection: %w", err)}
		}

		go rt.forward(remote)
	}
}

// keepalive sends periodic SSH keepalive requests to detect dead connections.
// On failure, it closes the listener and SSH connection so that connect()
// unblocks and the reconnect loop fires.
func (rt *ReverseTunnel) keepalive(conn gossh.Conn, stop <-chan struct{}) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-rt.done:
			return
		case <-stop:
			return
		case <-ticker.C:
			_, _, err := conn.SendRequest("keepalive@tw", true, nil)
			if err != nil {
				slog.Warn("reverse tunnel keepalive failed, triggering reconnect", "error", err)
				// Close listener first — this unblocks Accept() in connect().
				rt.mu.Lock()
				if rt.listener != nil {
					rt.listener.Close()
				}
				rt.mu.Unlock()
				conn.Close()
				return
			}
		}
	}
}

func (rt *ReverseTunnel) forward(remote net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic in reverse tunnel forward", "error", r)
		}
	}()
	defer remote.Close()

	local, err := net.DialTimeout("tcp", rt.LocalAddr, 10*time.Second)
	if err != nil {
		slog.Error("reverse tunnel failed to connect to local", "addr", rt.LocalAddr, "error", err)
		return
	}
	defer local.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(local, remote)
		if tc, ok := local.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	go func() {
		defer wg.Done()
		io.Copy(remote, local)
		if tc, ok := remote.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	wg.Wait()
}

// Stop shuts down the reverse tunnel.
func (rt *ReverseTunnel) Stop() {
	if rt.done != nil {
		close(rt.done)
	}
	rt.cleanup()
}
