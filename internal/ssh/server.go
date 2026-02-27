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

	gossh "golang.org/x/crypto/ssh"
)

// Server is an embedded SSH server used for relay-to-server connectivity.
type Server struct {
	Port           int
	HostKeyDir     string
	AuthorizedKeys string
	OnConnect      func(user string) // called after successful SSH authentication
	OnDisconnect   func(user string) // called when an SSH connection closes
	config         *gossh.ServerConfig
	listener       net.Listener
}

func NewServer(port int, hostKeyDir, authorizedKeys string) (*Server, error) {
	s := &Server{
		Port:           port,
		HostKeyDir:     hostKeyDir,
		AuthorizedKeys: authorizedKeys,
		config:         &gossh.ServerConfig{},
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
		pub, _, options, r, parseErr := gossh.ParseAuthorizedKey(rest)
		if parseErr != nil {
			break
		}
		rest = r

		if string(pub.Marshal()) != string(keyBytes) {
			continue
		}

		slog.Info("client authenticated", "user", conn.User(), "remote", conn.RemoteAddr())

		perms := &gossh.Permissions{
			Extensions: map[string]string{},
		}

		// Parse permitopen options for port forwarding restrictions.
		var permitOpens []string
		for _, opt := range options {
			if strings.HasPrefix(opt, `permitopen="`) {
				val := opt[len(`permitopen="`):]
				if idx := strings.Index(val, `"`); idx >= 0 {
					val = val[:idx]
				}
				permitOpens = append(permitOpens, val)
			}
		}
		if len(permitOpens) > 0 {
			perms.Extensions["permitopen"] = strings.Join(permitOpens, ",")
		}

		return perms, nil
	}

	return nil, fmt.Errorf("unknown public key for %q", conn.User())
}

func (s *Server) loadOrGenerateHostKey() error {
	keyPath := filepath.Join(s.HostKeyDir, "ssh_host_ed25519_key")

	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("reading host key: %w", err)
		}

		slog.Info("generating SSH host key", "path", keyPath)
		if err := os.MkdirAll(s.HostKeyDir, 0700); err != nil {
			return fmt.Errorf("creating host key directory: %w", err)
		}

		privPEM, _, err := GenerateKeyPair()
		if err != nil {
			return fmt.Errorf("generating host key: %w", err)
		}
		if err := os.WriteFile(keyPath, privPEM, 0600); err != nil {
			return fmt.Errorf("writing host key: %w", err)
		}
		keyData = privPEM
	}

	signer, err := gossh.ParsePrivateKey(keyData)
	if err != nil {
		return fmt.Errorf("parsing host key: %w", err)
	}

	s.config.AddHostKey(signer)
	return nil
}

// Run starts the SSH server (blocking). It survives transient accept errors
// and individual connection failures without stopping.
func (s *Server) Run() error {
	addr := fmt.Sprintf(":%d", s.Port)
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

	sshConn, chans, reqs, err := gossh.NewServerConn(conn, s.config)
	if err != nil {
		slog.Warn("SSH handshake failed", "error", err)
		return
	}
	defer sshConn.Close()

	user := sshConn.User()
	slog.Debug("SSH connection established", "remote", sshConn.RemoteAddr(), "client_version", sshConn.ClientVersion(), "user", user)

	if s.OnConnect != nil {
		s.OnConnect(user)
	}
	defer func() {
		if s.OnDisconnect != nil {
			s.OnDisconnect(user)
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

	slog.Debug("SSH connection closed", "remote", sshConn.RemoteAddr())
}

// directTCPIPData matches the RFC 4254 §7.2 payload for direct-tcpip channels.
type directTCPIPData struct {
	DestHost   string
	DestPort   uint32
	OriginHost string
	OriginPort uint32
}

func parseDirectTCPIP(data []byte) (directTCPIPData, error) {
	var d directTCPIPData
	if len(data) < 4 {
		return d, fmt.Errorf("data too short")
	}

	hostLen := binary.BigEndian.Uint32(data[0:4])
	if uint32(len(data)) < 4+hostLen+4+4+4 {
		return d, fmt.Errorf("data too short for dest host")
	}
	d.DestHost = string(data[4 : 4+hostLen])
	offset := 4 + hostLen
	d.DestPort = binary.BigEndian.Uint32(data[offset : offset+4])
	offset += 4

	origHostLen := binary.BigEndian.Uint32(data[offset : offset+4])
	offset += 4
	if uint32(len(data)) < offset+origHostLen+4 {
		return d, fmt.Errorf("data too short for origin host")
	}
	d.OriginHost = string(data[offset : offset+origHostLen])
	offset += origHostLen
	d.OriginPort = binary.BigEndian.Uint32(data[offset : offset+4])

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

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(conn, ch)
		// Half-close: signal the TCP side we're done writing.
		if tc, ok := conn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	go func() {
		defer wg.Done()
		io.Copy(ch, conn)
		ch.CloseWrite()
	}()

	wg.Wait()
}

// isPortAllowed checks whether a direct-tcpip destination is permitted
// by the authorized_keys entry's permitopen options.
// If no permitopen options are set, all destinations are allowed.
func isPortAllowed(perms *gossh.Permissions, host string, port uint32) bool {
	if perms == nil || perms.Extensions == nil {
		return true
	}
	permitted, ok := perms.Extensions["permitopen"]
	if !ok {
		return true // No restrictions — allow all.
	}
	target := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	for _, allowed := range strings.Split(permitted, ",") {
		if allowed == target {
			return true
		}
	}
	return false
}

// Stop gracefully stops the SSH server.
func (s *Server) Stop() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}
