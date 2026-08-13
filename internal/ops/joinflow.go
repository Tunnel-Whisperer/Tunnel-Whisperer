package ops

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/cryptobox"
	"github.com/tunnelwhisperer/tw/internal/enroll"
	"github.com/tunnelwhisperer/tw/internal/ops/modeauth"
	"github.com/tunnelwhisperer/tw/internal/pki"
	twssh "github.com/tunnelwhisperer/tw/internal/ssh"
)

// JoinUI carries the enrollee's human-in-the-loop callbacks.
type JoinUI struct {
	// ShowSAS displays the short authentication string to read to the issuer.
	ShowSAS func(sas string)
	// ResolvePort is called (client role) when a granted tunnel's local port
	// is already busy on this machine; it returns the replacement port.
	ResolvePort func(t config.Tunnel) int
}

// JoinResult describes the context Join produced.
type JoinResult struct {
	ContextName string
	Role        string
	Switched    bool
}

// Join enrolls this machine against relayHost using a spoken invite code —
// the issuer decides whether we become a server tenant or a client user. The
// result lands as a new stored context; if this machine has no configured
// profile yet, the context is activated too.
func (o *Ops) Join(ctx context.Context, relayHost, code, name string, ui JoinUI, progress ProgressFunc) (*JoinResult, error) {
	tok, err := enroll.ParseCode(code)
	if err != nil {
		return nil, err
	}
	httpc := &http.Client{Timeout: 30 * time.Second}
	base := fmt.Sprintf("https://%s/enroll/%s", relayHost, tok)

	var srvIdent *localServerIdentity
	var cli clientMaterial
	role, grant, err := enroll.RunEnrollee(ctx, httpc, base, code, tok, enroll.EnrolleeCallbacks{
		ShowSAS: ui.ShowSAS,
		MakeOffer: func(r enroll.RoleOffer) ([]byte, error) {
			switch r.Role {
			case "server":
				var err error
				if srvIdent, err = newLocalServerIdentity(); err != nil {
					return nil, err
				}
				req := &JoinRequest{
					Version: 1, ServerID: srvIdent.serverID, Hostname: srvIdent.hostname,
					UUID: srvIdent.uuid, RelayHost: relayHost,
					CACertPEM: string(srvIdent.caCert),
					SSHPubkey: strings.TrimSpace(string(srvIdent.sshPub)),
				}
				return json.Marshal(req)
			case "client":
				return cli.makeOffer(r.Username)
			default:
				return nil, fmt.Errorf("issuer offered unknown role %q", r.Role)
			}
		},
	})
	if err != nil {
		return nil, err
	}

	switch role.Role {
	case "server":
		if name == "" {
			name = config.DefaultContextName("server", relayHost, "")
		}
		return o.applyServerGrant(srvIdent, grant, name)
	case "client":
		if name == "" {
			name = role.Username
		}
		return o.applyClientGrant(&cli, grant, name, ui)
	}
	return nil, fmt.Errorf("unreachable role %q", role.Role)
}

// applyServerGrant turns an issuer's JoinResponse grant into a stored,
// mode-signed server context — the remote sibling of AddLocalServer's tail.
func (o *Ops) applyServerGrant(ident *localServerIdentity, grant []byte, name string) (*JoinResult, error) {
	resp, err := DecodeJoinResponse(grant)
	if err != nil {
		return nil, fmt.Errorf("issuer sent an invalid grant: %w", err)
	}
	scfg, err := buildLocalServerConfig(ident, resp, o.Config())
	if err != nil {
		return nil, err
	}
	bundle, err := sealLocalServerBundle(ident, scfg)
	if err != nil {
		return nil, err
	}
	if _, err := o.ImportContext(bundle, name, false); err != nil {
		return nil, err
	}
	res := &JoinResult{ContextName: name, Role: "server"}
	if liveProfileEmpty() {
		if err := o.UseContext(name, nil); err != nil {
			return res, fmt.Errorf("context %q stored but activating it failed: %w", name, err)
		}
		res.Switched = true
	}
	return res, nil
}

// clientMaterial is the client-role enrollee's locally-born identity: SSH
// keypair, cert key + CSR, UUID. Private halves never leave this machine.
type clientMaterial struct {
	uuid    string
	sshPriv []byte
	sshPub  []byte
	certKey []byte
	csrPEM  []byte
}

func (c *clientMaterial) makeOffer(username string) ([]byte, error) {
	if username == "" {
		return nil, fmt.Errorf("issuer sent a client role without a username")
	}
	c.uuid = uuid.New().String()
	var err error
	if c.sshPriv, c.sshPub, err = twssh.GenerateKeyPair(); err != nil {
		return nil, fmt.Errorf("generating SSH key pair: %w", err)
	}
	if c.certKey, c.csrPEM, err = pki.GenerateKeyAndCSR(username); err != nil {
		return nil, fmt.Errorf("generating certificate request: %w", err)
	}
	return json.Marshal(enroll.ClientOffer{
		Username: username, UUID: c.uuid,
		SSHPubkey: strings.TrimSpace(string(c.sshPub)),
		CSRPEM:    string(c.csrPEM),
	})
}

// applyClientGrant turns the issuer's grant into a stored client context:
// preflights every granted local port on THIS machine (prompting for
// replacements), verifies the mode signature against our own pubkey, and
// seals config + keys + signed cert into a context bundle.
func (o *Ops) applyClientGrant(c *clientMaterial, grant []byte, name string, ui JoinUI) (*JoinResult, error) {
	var g enroll.ClientGrant
	if err := json.Unmarshal(grant, &g); err != nil {
		return nil, fmt.Errorf("issuer sent an invalid grant: %w", err)
	}
	if g.RelayHost == "" || g.SSHUser == "" || g.ClientCertPEM == "" {
		return nil, fmt.Errorf("issuer grant is incomplete")
	}
	ccfg := config.Default()
	ccfg.Mode = "client"
	ccfg.Xray.UUID = c.uuid
	ccfg.Xray.RelayHost = g.RelayHost
	ccfg.Xray.RelayPort = g.RelayPort
	ccfg.Xray.Path = g.Path
	ccfg.Client.SSHUser = g.SSHUser
	ccfg.Client.ServerSSHPort = g.ServerSSHPort
	for _, gt := range g.Tunnels {
		t := config.Tunnel{LocalPort: gt.LocalPort, RemoteHost: gt.RemoteHost, RemotePort: gt.RemotePort}
		// Preflight: the port must be bindable HERE, now — the whole point of
		// locally-born configs (port_overrides remains for later changes).
		if !loopbackPortFree(t.LocalPort) && ui.ResolvePort != nil {
			if p := ui.ResolvePort(t); p > 0 {
				t.LocalPort = p
			}
		}
		ccfg.Client.Tunnels = append(ccfg.Client.Tunnels, t)
	}
	if g.ModeSig != "" && g.ModeIssuer != "" {
		if err := modeauth.Verify("client", strings.TrimSpace(string(c.sshPub)), g.ModeSig, g.ModeIssuer); err != nil {
			slog.Warn("mode signature from invite does not verify; storing profile unsigned", "error", err)
		} else {
			ccfg.ModeAuth = &config.ModeAuth{Sig: g.ModeSig, Issuer: g.ModeIssuer}
		}
	}
	cfgYAML, err := yaml.Marshal(ccfg)
	if err != nil {
		return nil, err
	}
	entries := []struct {
		name string
		data []byte
	}{
		{"config.yaml", cfgYAML},
		{"id_ed25519", c.sshPriv},
		{"id_ed25519.pub", c.sshPub},
		{"client.crt", []byte(g.ClientCertPEM)},
		{"client.key", c.certKey},
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(e.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	sealed, err := cryptobox.Encrypt(buf.Bytes(), "")
	if err != nil {
		return nil, fmt.Errorf("sealing client context: %w", err)
	}
	if _, err := o.ImportContext(sealed, name, false); err != nil {
		return nil, err
	}
	res := &JoinResult{ContextName: name, Role: "client"}
	if liveProfileEmpty() {
		if err := o.UseContext(name, nil); err != nil {
			return res, fmt.Errorf("context %q stored but activating it failed: %w", name, err)
		}
		res.Switched = true
	}
	return res, nil
}
