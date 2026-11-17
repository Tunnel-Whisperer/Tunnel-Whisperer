package ops

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"

	"regexp"
)

// joinServerIDRe is the server-id shape the relay renderers also enforce
// (lowercase alphanumeric + hyphen, starting alphanumeric).
var joinServerIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// maxServerIDLen is the X.509 upper bound for a CommonName (RFC 5280
// ub-common-name); the server id must equal its CA's subject CN.
const maxServerIDLen = 64

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

// validateServerIdentity checks the tenant identity a join request carries and
// the registry stores — everything that is rendered into the relay's
// authorized_keys, Xray config and Caddy trust pool — and returns the
// canonical CA PEM and ssh public key. Errors read as "<field> ...".
func validateServerIdentity(serverID, uuid, caCertPEM, sshPubkey string) (caPEM, pubKey string, err error) {
	if serverID == "" || uuid == "" {
		return "", "", fmt.Errorf("missing server_id/uuid")
	}
	// server_id must match the same shape the relay renderers enforce, so a
	// malformed id can't be enrolled (which would burn a port + orphan a
	// registry entry before the render rejects it).
	if !joinServerIDRe.MatchString(serverID) {
		return "", "", fmt.Errorf("server_id %q has an invalid shape", serverID)
	}
	if len(serverID) > maxServerIDLen {
		return "", "", fmt.Errorf("server_id is %d characters; the limit is %d", len(serverID), maxServerIDLen)
	}
	// A UUID-shaped server_id would equal the email of a client the tenant
	// can add to its own inbound, and Xray refuses an inbound with duplicate
	// emails, so it is rejected here as in every relay renderer.
	if isCanonicalUUID(serverID) {
		return "", "", fmt.Errorf("server_id %q must not be shaped like a UUID", serverID)
	}
	// The UUID is rendered into the relay's Xray config; a malformed one would
	// be registered and then break every later render.
	if !isCanonicalUUID(uuid) {
		return "", "", fmt.Errorf("uuid %q is not a canonical UUID", uuid)
	}
	// Exactly one CERTIFICATE block: the whole string lands in the relay's
	// shared trust pool, so nothing beyond the validated certificate may ride
	// along with it.
	blk, rest := pem.Decode([]byte(caCertPEM))
	if blk == nil || blk.Type != "CERTIFICATE" {
		return "", "", fmt.Errorf("ca_cert_pem is not a PEM certificate")
	}
	if strings.TrimSpace(string(rest)) != "" {
		return "", "", fmt.Errorf("ca_cert_pem must contain exactly one certificate")
	}
	crt, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return "", "", fmt.Errorf("ca_cert_pem is not a certificate: %w", err)
	}
	// Path length 0 (what pki.GenerateCA issues): the Caddyfile tells tenants
	// apart by leaf subject/issuer strings, which only holds if no tenant CA
	// can sign an intermediate carrying another tenant's name.
	if !crt.BasicConstraintsValid || !crt.IsCA || !(crt.MaxPathLenZero || crt.MaxPathLen == 0) {
		return "", "", fmt.Errorf("ca_cert_pem must be a CA certificate with path length 0")
	}
	// The relay admits every tenant CA through one shared trust pool and tells
	// them apart only by the client-cert CN. Bind the CA's own subject to this
	// server's id so a tenant cannot enroll a CA whose subject impersonates
	// another tenant's id — which, combined with the per-route issuer check in
	// the Caddyfile, is what stops cross-tenant impersonation (finding #10).
	if crt.Subject.CommonName != serverID {
		return "", "", fmt.Errorf("ca_cert_pem subject CN %q must equal server_id %q", crt.Subject.CommonName, serverID)
	}
	// Canonicalize and reject anything but a single key on one line, so the
	// registry/render never sees the raw (potentially multi-line) input.
	pubKey, err = canonicalAuthorizedKey(sshPubkey)
	if err != nil {
		return "", "", fmt.Errorf("ssh_pubkey invalid: %w", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: blk.Bytes})), pubKey, nil
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
	ca, key, err := validateServerIdentity(r.ServerID, r.UUID, r.CACertPEM, r.SSHPubkey)
	if err != nil {
		return nil, fmt.Errorf("join request %w", err)
	}
	r.CACertPEM, r.SSHPubkey = ca, key
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
