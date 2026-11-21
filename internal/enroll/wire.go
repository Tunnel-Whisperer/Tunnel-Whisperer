package enroll

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// startReq/startResp, boxMsg are the wire envelopes.
type startReq struct {
	Pake string `json:"pake"` // base64 std
}
type startResp struct {
	Pake string `json:"pake"`
	Box  string `json:"box"` // boxed RoleOffer
}
type boxMsg struct {
	Box string `json:"box"`
}

// resultProofHeader carries, on GET /result, a box sealed under the session
// key with resultProofPlaintext. The issuer counts the grant as collected
// only for a poll that carries a valid proof. Issuers that predate it ignore
// the header.
const (
	resultProofHeader    = "X-TW-Enroll-Proof"
	resultProofPlaintext = "tw-enroll result v1"
)

// offerV2Field is prepended to the JSON object of the enrollee's offer, inside
// the sealed box, to announce that its /result polls carry the proof. Only the
// PAKE-key holder can produce a box that opens, so the announcement cannot be
// forged or stripped by anyone holding just the route token. Issuers that
// predate it pass the field on to a json.Unmarshal into a struct, which
// ignores it.
const offerV2Field = `{"tw_enroll_v":2`

// markOfferV2 prepends offerV2Field to offer, which must be a JSON object.
func markOfferV2(offer []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(offer)
	if len(trimmed) < 2 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return nil, fmt.Errorf("enrollment offer is not a JSON object")
	}
	rest := bytes.TrimSpace(trimmed[1:])
	if rest[0] != '}' {
		rest = append([]byte{','}, rest...)
	}
	return append([]byte(offerV2Field), rest...), nil
}

// unmarkOfferV2 reports whether plain carries offerV2Field and returns the
// offer as the enrollee's MakeOffer produced it (modulo whitespace).
func unmarkOfferV2(plain []byte) ([]byte, bool) {
	rest, ok := bytes.CutPrefix(plain, []byte(offerV2Field))
	if !ok || len(rest) == 0 {
		return plain, false
	}
	switch rest[0] {
	case '}':
		return append([]byte{'{'}, rest...), true
	case ',':
		return append([]byte{'{'}, rest[1:]...), true
	}
	return plain, false
}

type RoleOffer struct {
	Role     string `json:"role"` // "server" or "client"
	Username string `json:"username,omitempty"`
}

type ClientOffer struct {
	Username  string `json:"username"`
	UUID      string `json:"uuid"`
	SSHPubkey string `json:"ssh_pubkey"`
	CSRPEM    string `json:"csr_pem"`
}

type GrantTunnel struct {
	LocalPort  int    `json:"local_port"`
	RemoteHost string `json:"remote_host"`
	RemotePort int    `json:"remote_port"`
}

type ClientGrant struct {
	RelayHost     string        `json:"relay_host"`
	RelayPort     int           `json:"relay_port"`
	Path          string        `json:"path"`
	SSHUser       string        `json:"ssh_user"`
	ServerSSHPort int           `json:"server_ssh_port"`
	ServerHostKey string        `json:"server_host_key,omitempty"` // server SSH host public key (authorized_keys format), pinned by the client
	Tunnels       []GrantTunnel `json:"tunnels"`
	ClientCertPEM string        `json:"client_cert_pem"`
}
