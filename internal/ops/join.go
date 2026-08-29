package ops

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"

	"regexp"
)

// joinServerIDRe is the server-id shape the relay renderers also enforce
// (lowercase alphanumeric + hyphen, starting alphanumeric).
var joinServerIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

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
	ModeSig    string `json:"mode_sig,omitempty"`
	ModeIssuer string `json:"mode_issuer,omitempty"`
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
	// server_id must match the same shape the relay renderers enforce, so a
	// malformed id can't be enrolled (which would burn a port + orphan a
	// registry entry before the render rejects it).
	if !joinServerIDRe.MatchString(r.ServerID) {
		return nil, fmt.Errorf("join request server_id %q has an invalid shape", r.ServerID)
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
	// The relay admits every tenant CA through one shared trust pool and tells
	// them apart only by the client-cert CN. Bind the CA's own subject to this
	// server's id so a tenant cannot enroll a CA whose subject impersonates
	// another tenant's id — which, combined with the per-route issuer check in
	// the Caddyfile, is what stops cross-tenant impersonation (finding #10).
	if crt.Subject.CommonName != r.ServerID {
		return nil, fmt.Errorf("join request ca_cert_pem subject CN %q must equal server_id %q", crt.Subject.CommonName, r.ServerID)
	}
	// Canonicalize and reject anything but a single key on one line, and store
	// the normalized form so the downstream registry/render never sees the raw
	// (potentially multi-line) input.
	canon, err := canonicalAuthorizedKey(r.SSHPubkey)
	if err != nil {
		return nil, fmt.Errorf("join request ssh_pubkey invalid: %w", err)
	}
	r.SSHPubkey = canon
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

