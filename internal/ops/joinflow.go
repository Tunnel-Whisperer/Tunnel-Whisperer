package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/enroll"
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

// clientMaterial and applyClientGrant land in the client-role task; keep
// compile-green stubs here until then.
type clientMaterial struct{}

func (c *clientMaterial) makeOffer(username string) ([]byte, error) {
	return nil, fmt.Errorf("client enrollment: implemented in the next task")
}

func (o *Ops) applyClientGrant(c *clientMaterial, grant []byte, name string, ui JoinUI) (*JoinResult, error) {
	return nil, fmt.Errorf("client enrollment: implemented in the next task")
}
