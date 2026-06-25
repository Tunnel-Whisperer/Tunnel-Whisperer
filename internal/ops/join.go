package ops

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"github.com/tunnelwhisperer/tw/internal/config"
)

// JoinRequest is the public artifact a joining server hands to the admin.
type JoinRequest struct {
	Version   int    `json:"version"`
	ServerID  string `json:"server_id"`
	Hostname  string `json:"hostname"`
	UUID      string `json:"uuid"`
	RelayHost string `json:"relay_host"`
	CACertPEM string `json:"ca_cert_pem"`
	SSHPubkey string `json:"ssh_pubkey"`
}

// JoinResponse is what the admin hands back after enrollment.
type JoinResponse struct {
	Version    int    `json:"version"`
	ServerID   string `json:"server_id"`
	RelayHost  string `json:"relay_host"`
	Path       string `json:"path"`
	RemotePort int    `json:"remote_port"`
	SSHUser    string `json:"ssh_user"`
}

func (r *JoinRequest) Encode() ([]byte, error)  { return json.MarshalIndent(r, "", "  ") }
func (r *JoinResponse) Encode() ([]byte, error) { return json.MarshalIndent(r, "", "  ") }

// DecodeJoinRequest parses and validates a join-request (public material only).
func DecodeJoinRequest(b []byte) (*JoinRequest, error) {
	var r JoinRequest
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("parsing join request: %w", err)
	}
	if r.Version != 1 {
		return nil, fmt.Errorf("unsupported join-request version %d", r.Version)
	}
	if r.ServerID == "" || r.UUID == "" {
		return nil, fmt.Errorf("join request missing server_id/uuid")
	}
	blk, _ := pem.Decode([]byte(r.CACertPEM))
	if blk == nil {
		return nil, fmt.Errorf("join request ca_cert_pem is not valid PEM")
	}
	crt, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("join request ca_cert_pem is not a certificate: %w", err)
	}
	if !crt.IsCA {
		return nil, fmt.Errorf("join request ca_cert_pem is not a CA certificate")
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(r.SSHPubkey)); err != nil {
		return nil, fmt.Errorf("join request ssh_pubkey invalid: %w", err)
	}
	return &r, nil
}

// DecodeJoinResponse parses and validates a join-response.
func DecodeJoinResponse(b []byte) (*JoinResponse, error) {
	var r JoinResponse
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("parsing join response: %w", err)
	}
	if r.Version != 1 || r.RelayHost == "" || r.Path == "" || r.RemotePort == 0 {
		return nil, fmt.Errorf("join response is incomplete")
	}
	return &r, nil
}

// GenerateJoinRequest sets this machine up as a server joining relayHost: it
// generates the server's identity (ssh key + CA + client cert with CN=server-id),
// persists mode=server, relay host, derived path, and returns the public
// join-request artifact (CA cert PEM + SSH public key).
func (o *Ops) GenerateJoinRequest(relayHost string) (*JoinRequest, error) {
	// Seed UUID and mode under the lock, then release before calling methods
	// that also acquire o.mu to avoid deadlock.
	o.mu.Lock()
	if o.cfg.Mode == "" {
		o.cfg.Mode = "server"
	}
	if o.cfg.Xray.UUID == "" {
		o.cfg.Xray.UUID = uuid.New().String()
	}
	uid := o.cfg.Xray.UUID
	cfg := o.cfg
	o.mu.Unlock()

	// Persist mode + UUID immediately so ensureCerts picks up the right UUID.
	if err := config.Save(cfg); err != nil {
		return nil, fmt.Errorf("persisting initial config: %w", err)
	}

	host, _ := os.Hostname()
	serverID := deriveServerID(host, uid)
	path := "/tw/" + serverID

	// Persist relay host + path (SetXraySettings skips UUID, which is already saved above).
	if err := o.SetXraySettings(config.XrayConfig{RelayHost: relayHost, Path: path}); err != nil {
		return nil, fmt.Errorf("persisting relay config: %w", err)
	}
	// EnsureKeys calls ensureCerts internally; ensureCerts derives CN from deriveServerID(host, uuid).
	if err := o.EnsureKeys(); err != nil {
		return nil, fmt.Errorf("generating identity: %w", err)
	}

	caPEM, err := os.ReadFile(config.CACertPath())
	if err != nil {
		return nil, fmt.Errorf("reading CA cert: %w", err)
	}
	pub, err := os.ReadFile(filepath.Join(config.Dir(), "id_ed25519.pub"))
	if err != nil {
		return nil, fmt.Errorf("reading ssh pubkey: %w", err)
	}
	return &JoinRequest{
		Version:   1,
		ServerID:  serverID,
		Hostname:  host,
		UUID:      uid,
		RelayHost: relayHost,
		CACertPEM: string(caPEM),
		SSHPubkey: strings.TrimSpace(string(pub)),
	}, nil
}

// ApplyJoinResponse configures this server with the admin-assigned coordinates.
func (o *Ops) ApplyJoinResponse(r *JoinResponse) error {
	if err := o.SetXraySettings(config.XrayConfig{RelayHost: r.RelayHost, Path: r.Path}); err != nil {
		return fmt.Errorf("persisting relay coords: %w", err)
	}
	if err := o.SetServerSettings(config.ServerConfig{RemotePort: r.RemotePort, RelaySSHUser: r.SSHUser}); err != nil {
		return fmt.Errorf("persisting remote port: %w", err)
	}
	return nil
}
