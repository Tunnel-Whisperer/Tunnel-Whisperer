package ops

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"

	"golang.org/x/crypto/ssh"
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
