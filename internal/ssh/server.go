// Package ssh implements the embedded SSH server and the forward and reverse
// tunnels that carry traffic end-to-end through the relay. The server enforces
// per-user access control from an authorized_keys file re-read on every auth
// attempt, gating port forwarding against each key's permitopen options.
package ssh

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tunnelwhisperer/tw/internal/stats"
	gossh "golang.org/x/crypto/ssh"
)

const (
	// handshakeTimeout bounds the pre-auth SSH handshake so a client that opens
	// a connection and then stalls cannot pin a goroutine/fd indefinitely
	// (Slowloris). Cleared once the handshake completes — an authenticated
	// tunnel is long-lived.
	handshakeTimeout = 30 * time.Second
	// maxConcurrentHandshakes caps in-flight (pre-auth) handshakes. Beyond this,
	// new connections are dropped immediately rather than piling up goroutines,
	// blunting a connection flood. Legitimate handshakes are brief, so this is
	// far above real concurrency; the slot is released as soon as auth settles.
	maxConcurrentHandshakes = 64
)

// Server is an embedded SSH server used for relay-to-server connectivity.
type Server struct {
	Port           int
	HostKeyDir     string
	AuthorizedKeys string
	OnConnect      func(user string) // called after successful SSH authentication
	OnDisconnect   func(user string) // called when an SSH connection closes
	Stats          *stats.Collector  // nil = disabled, no overhead
	config         *gossh.ServerConfig
	listener       net.Listener
	handshakeSem   chan struct{} // bounds concurrent pre-auth handshakes

	connMu       sync.Mutex
	connectedMap map[string]int // tw_user → active session count
}

func NewServer(port int, hostKeyDir, authorizedKeys string) (*Server, error) {
	s := &Server{
		Port:           port,
		HostKeyDir:     hostKeyDir,
		AuthorizedKeys: authorizedKeys,
		config:         &gossh.ServerConfig{},
		connectedMap:   make(map[string]int),
		handshakeSem:   make(chan struct{}, maxConcurrentHandshakes),
	}

	if err := s.loadAuthorizedKeys(); err != nil {
		return nil, err
	}

	if err := s.loadOrGenerateHostKey(); err != nil {
		return nil, err
	}

	return s, nil
}

// loadAuthorizedKeys sets up dynamic public key authentication.
// The authorized_keys file is re-read on each authentication attempt,
// so adding or removing keys takes effect without restarting the server.
func (s *Server) loadAuthorizedKeys() error {
	if _, err := os.Stat(s.AuthorizedKeys); err != nil {
		if os.IsNotExist(err) {
			slog.Warn("no authorized_keys file, clients can connect once it is created", "path", s.AuthorizedKeys)
		}
	}

	s.config.PublicKeyCallback = func(conn gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
		return s.checkAuthorizedKey(conn, key)
	}

	return nil
}

// checkAuthorizedKey reads the authorized_keys file and checks if the
// given public key is allowed. It also parses permitopen options for
// port forwarding restrictions.
func (s *Server) checkAuthorizedKey(conn gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
	data, err := os.ReadFile(s.AuthorizedKeys)
	if err != nil {
		return nil, fmt.Errorf("reading authorized_keys: %w", err)
	}

	keyBytes := key.Marshal()
	rest := data
	for len(rest) > 0 {
		pub, comment, options, r, parseErr := gossh.ParseAuthorizedKey(rest)
		if parseErr != nil {
			break
		}
		rest = r

		if string(pub.Marshal()) != string(keyBytes) {
			continue
		}

		// Extract TW username from comment (format: "username@tw").
		twUser := strings.TrimSuffix(comment, "@tw")

		slog.Info("client authenticated", "user", twUser, "ssh_user", conn.User(), "remote", conn.RemoteAddr().String())

		perms := &gossh.Permissions{
			Extensions: map[string]string{},
		}

		if twUser != "" {
			perms.Extensions["tw_user"] = twUser
		}

		// Parse permitopen and single-session options.
		var permitOpens []string
		singleSession := false
		for _, opt := range options {
			if strings.HasPrefix(opt, `permitopen="`) {
				val := opt[len(`permitopen="`):]
				if idx := strings.Index(val, `"`); idx >= 0 {
					val = val[:idx]
				}
				permitOpens = append(permitOpens, val)
			}
			if opt == "single-session" {
				singleSession = true
			}
		}
		if len(permitOpens) > 0 {
			perms.Extensions["permitopen"] = strings.Join(permitOpens, ",")
		}
		if singleSession && twUser != "" {
			perms.Extensions["single-session"] = "1"
		}

		// Fast-path reject if the user already has an active session. This is a
		// best-effort early check; the AUTHORITATIVE, race-free enforcement is
		// the atomic check-and-increment under connMu in handleConnection
		// (finding #8 — the count is only incremented after the handshake, so a
		// check here alone is a TOCTOU).
		if singleSession && twUser != "" {
			s.connMu.Lock()
			count := s.connectedMap[twUser]
			s.connMu.Unlock()
			if count > 0 {
				slog.Warn("single-session: rejecting duplicate connection", "tw_user", twUser)
				return nil, fmt.Errorf("user %q already has an active session (single-session enabled)", twUser)
			}
		}

		return perms, nil
	}

	return nil, fmt.Errorf("unknown public key for %q", conn.User())
}

func (s *Server) loadOrGenerateHostKey() error {
	signer, err := loadOrGenerateHostSigner(s.HostKeyDir)
	if err != nil {
		return err
	}
	s.config.AddHostKey(signer)
	return nil
}

// loadOrGenerateHostSigner loads the server's SSH host key from dir, generating
// and persisting a new one if none exists. It is shared by the running SSH
// server and by enrollment, so both agree on the same host identity.
func loadOrGenerateHostSigner(dir string) (gossh.Signer, error) {
	keyPath := filepath.Join(dir, "ssh_host_ed25519_key")

	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading host key: %w", err)
		}

		slog.Info("generating SSH host key", "path", keyPath)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("creating host key directory: %w", err)
		}

		privPEM, _, err := GenerateKeyPair()
		if err != nil {
			return nil, fmt.Errorf("generating host key: %w", err)
		}
		if err := os.WriteFile(keyPath, privPEM, 0600); err != nil {
			return nil, fmt.Errorf("writing host key: %w", err)
		}
		keyData = privPEM
	}

	signer, err := gossh.ParsePrivateKey(keyData)
	if err != nil {
		return nil, fmt.Errorf("parsing host key: %w", err)
	}
	return signer, nil
}

// EnsureHostPublicKey returns the server's SSH host public key in
// authorized_keys format, generating the host key if it does not yet exist.
// Clients pin this value (via FixedHostKey) so a compromised or malicious relay
// cannot terminate and MITM the end-to-end SSH session.
func EnsureHostPublicKey(dir string) (string, error) {
	signer, err := loadOrGenerateHostSigner(dir)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(gossh.MarshalAuthorizedKey(signer.PublicKey()))), nil
}

// Run starts the SSH server (blocking). It survives transient accept errors
// and individual connection failures without stopping.
func (s *Server) Run() error {
	// Bind loopback only. The sole legitimate consumer is the reverse tunnel,
	// which dials 127.0.0.1:<port> from this same host and republishes the port
	// on the relay; binding all interfaces needlessly exposed the pre-auth
	// handshake surface to the LAN (finding SP-11).
	addr := fmt.Sprintf("127.0.0.1:%d", s.Port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("ssh-server: listen %s: %w", addr, err)
	}
	s.listener = lis

	slog.Info("SSH server listening", "addr", addr)

	for {
		conn, err := lis.Accept()
		if err != nil {
			// If the listener was closed (Stop was called), exit cleanly.
			if errors.Is(err, net.ErrClosed) {
				slog.Info("SSH server listener closed, shutting down")
				return nil
			}
			// Transient error — log and keep accepting.
			slog.Warn("SSH server accept error, continuing", "error", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// Enable TCP keepalive to detect dead connections.
		if tc, ok := conn.(*net.TCPConn); ok {
			tc.SetKeepAlive(true)
			tc.SetKeepAlivePeriod(30 * time.Second)
		}

		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic in SSH connection handler", "error", r)
		}
	}()

	// Bound concurrent pre-auth handshakes: drop new connections when too many
	// are already mid-handshake rather than piling up goroutines (SP-10). The
	// slot is held only for the handshake, not the authenticated session.
	select {
	case s.handshakeSem <- struct{}{}:
	default:
		slog.Warn("SSH server: too many concurrent handshakes, dropping connection", "remote", conn.RemoteAddr().String())
		return
	}

	// Deadline the handshake so a stalled client cannot pin the connection
	// (Slowloris); clear it afterwards — an authenticated tunnel is long-lived.
	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))
	sshConn, chans, reqs, err := gossh.NewServerConn(conn, s.config)
	_ = conn.SetDeadline(time.Time{})
	<-s.handshakeSem // release the pre-auth slot once the handshake settles
	if err != nil {
		slog.Warn("SSH handshake failed", "error", err)
		return
	}
	defer sshConn.Close()

	// Use TW username from auth if available, otherwise fall back to SSH user.
	twUser := ""
	if sshConn.Permissions != nil {
		twUser = sshConn.Permissions.Extensions["tw_user"]
	}
	displayUser := twUser
	if displayUser == "" {
		displayUser = sshConn.User()
	}

	slog.Debug("SSH connection established", "remote", sshConn.RemoteAddr().String(), "client_version", string(sshConn.ClientVersion()), "user", displayUser)

	// Track active sessions per TW user.
	if twUser != "" {
		singleSession := sshConn.Permissions != nil && sshConn.Permissions.Extensions["single-session"] == "1"
		s.connMu.Lock()
		// Atomic check-and-increment closes the single-session TOCTOU (#8): the
		// auth-time check and this increment now happen under the same lock, so
		// two connections racing in cannot both observe a zero count.
		if singleSession && s.connectedMap[twUser] > 0 {
			s.connMu.Unlock()
			slog.Warn("single-session: rejecting duplicate connection", "tw_user", twUser, "remote", sshConn.RemoteAddr().String())
			return
		}
		s.connectedMap[twUser]++
		count := s.connectedMap[twUser]
		s.connMu.Unlock()
		slog.Debug("session tracked", "user", twUser, "sessions", count, "remote", sshConn.RemoteAddr().String())
		defer func() {
			s.connMu.Lock()
			s.connectedMap[twUser]--
			remaining := s.connectedMap[twUser]
			if remaining <= 0 {
				delete(s.connectedMap, twUser)
				remaining = 0
			}
			s.connMu.Unlock()
			slog.Debug("session untracked", "user", twUser, "sessions", remaining, "remote", sshConn.RemoteAddr().String())
		}()
	}

	if s.OnConnect != nil {
		s.OnConnect(displayUser)
	}
	defer func() {
		if s.OnDisconnect != nil {
			s.OnDisconnect(displayUser)
		}
	}()

	go gossh.DiscardRequests(reqs)

	for newChan := range chans {
		switch newChan.ChannelType() {
		case "direct-tcpip":
			go s.handleDirectTCPIP(newChan, sshConn.Permissions)
		default:
			newChan.Reject(gossh.UnknownChannelType, fmt.Sprintf("unsupported channel type: %s", newChan.ChannelType()))
		}
	}

	slog.Debug("SSH connection closed", "remote", sshConn.RemoteAddr().String())
}

// directTCPIPData matches the RFC 4254 §7.2 payload for direct-tcpip channels.
type directTCPIPData struct {
	DestHost   string
	DestPort   uint32
	OriginHost string
	OriginPort uint32
}

func parseDirectTCPIP(data []byte) (directTCPIPData, error) {
	// All length/offset math is done in int/uint64 and compared against the
	// real buffer length, so an attacker-supplied 32-bit length near 2^32 can
	// never wrap a uint32 sum and slip a slice past the end of the buffer
	// (finding SP-13). off is a plain int into a real (small) []byte.
	var d directTCPIPData
	off := 0
	readStr := func() (string, error) {
		if off+4 > len(data) {
			return "", fmt.Errorf("truncated length prefix")
		}
		n := binary.BigEndian.Uint32(data[off : off+4])
		start := off + 4
		if uint64(start)+uint64(n) > uint64(len(data)) {
			return "", fmt.Errorf("string length %d exceeds %d remaining bytes", n, len(data)-start)
		}
		off = start + int(n)
		return string(data[start:off]), nil
	}
	readU32 := func() (uint32, error) {
		if off+4 > len(data) {
			return 0, fmt.Errorf("truncated uint32")
		}
		v := binary.BigEndian.Uint32(data[off : off+4])
		off += 4
		return v, nil
	}
	var err error
	if d.DestHost, err = readStr(); err != nil {
		return d, err
	}
	if d.DestPort, err = readU32(); err != nil {
		return d, err
	}
	if d.OriginHost, err = readStr(); err != nil {
		return d, err
	}
	if d.OriginPort, err = readU32(); err != nil {
		return d, err
	}
	return d, nil
}

func (s *Server) handleDirectTCPIP(newChan gossh.NewChannel, perms *gossh.Permissions) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic in direct-tcpip handler", "error", r)
		}
	}()

	d, err := parseDirectTCPIP(newChan.ExtraData())
	if err != nil {
		newChan.Reject(gossh.ConnectionFailed, fmt.Sprintf("invalid direct-tcpip data: %v", err))
		return
	}

	dest := net.JoinHostPort(d.DestHost, fmt.Sprintf("%d", d.DestPort))

	// Check port forwarding restrictions from authorized_keys permitopen options.
	if !isPortAllowed(perms, d.DestHost, d.DestPort) {
		slog.Warn("direct-tcpip denied, not in permitopen", "origin", fmt.Sprintf("%s:%d", d.OriginHost, d.OriginPort), "dest", dest)
		newChan.Reject(gossh.Prohibited, "port forwarding to this destination is not permitted")
		return
	}

	slog.Debug("direct-tcpip forwarding", "origin", fmt.Sprintf("%s:%d", d.OriginHost, d.OriginPort), "dest", dest)

	conn, err := net.DialTimeout("tcp", dest, 10*time.Second)
	if err != nil {
		newChan.Reject(gossh.ConnectionFailed, fmt.Sprintf("dial %s: %v", dest, err))
		return
	}
	defer conn.Close()

	// Enable TCP keepalive on the forwarded connection too.
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(30 * time.Second)
	}

	ch, _, err := newChan.Accept()
	if err != nil {
		slog.Warn("SSH channel accept failed", "error", err)
		return
	}
	defer ch.Close()

	// Track connection and bandwidth if stats are enabled.
	var sentW, recvW io.Writer = conn, ch
	if s.Stats != nil {
		twUser := ""
		if perms != nil {
			twUser = perms.Extensions["tw_user"]
		}
		key := stats.TunnelKey{User: twUser, Port: int(d.DestPort)}
		closeConn := s.Stats.TrackConn(key)
		defer closeConn()
		ts := s.Stats.Get(key)
		sentW = stats.NewCountingWriter(conn, &ts.BytesSent)
		recvW = stats.NewCountingWriter(ch, &ts.BytesRecv)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(sentW, ch)
		// Half-close: signal the TCP side we're done writing.
		if tc, ok := conn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	go func() {
		defer wg.Done()
		io.Copy(recvW, conn)
		ch.CloseWrite()
	}()

	wg.Wait()
}

// isPortAllowed checks whether a direct-tcpip destination is permitted
// by the authorized_keys entry's permitopen options.
// If no permitopen options are set, all destinations are allowed.
func isPortAllowed(perms *gossh.Permissions, host string, port uint32) bool {
	// Fail CLOSED: a key with no permitopen extension forwards nowhere, rather
	// than everywhere (finding SP-20). Every enrolled user key carries at least
	// one permitopen (appendAuthorizedKey), so this only denies a key that was
	// created with no port mappings — which should reach nothing.
	if perms == nil || perms.Extensions == nil {
		return false
	}
	permitted, ok := perms.Extensions["permitopen"]
	if !ok || permitted == "" {
		return false
	}
	target := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	for _, allowed := range strings.Split(permitted, ",") {
		if allowed == target {
			return true
		}
	}
	return false
}

// ConnectedUsers returns a snapshot of tw_user → active session count.
func (s *Server) ConnectedUsers() map[string]int {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	out := make(map[string]int, len(s.connectedMap))
	for k, v := range s.connectedMap {
		out[k] = v
	}
	return out
}

// Stop gracefully stops the SSH server.
func (s *Server) Stop() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}
