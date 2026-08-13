package ops

import (
	"archive/zip"
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/cryptobox"
	"github.com/tunnelwhisperer/tw/internal/ops/modeauth"
	"github.com/tunnelwhisperer/tw/internal/pki"
	twssh "github.com/tunnelwhisperer/tw/internal/ssh"
)

// LocalServerResult describes the server context created by AddLocalServer.
type LocalServerResult struct {
	ContextName   string
	ServerID      string
	RemotePort    int
	DashboardPort int
	APIPort       int
}

// localServerIdentity is a complete server identity generated in memory. It is
// never written into the live (relay) profile — it exists only to be enrolled
// and then sealed into the new context's bundle.
type localServerIdentity struct {
	uuid     string
	hostname string
	serverID string
	sshPriv  []byte
	sshPub   []byte
	caCert   []byte
	caKey    []byte
	client   []byte
	clientK  []byte
}

func newLocalServerIdentity() (*localServerIdentity, error) {
	uid := uuid.New().String()
	host, _ := os.Hostname()
	// A fresh UUID guarantees the derived id differs from the relay's own
	// identity, so AddServer's self-enroll guard passes by construction.
	id := deriveServerID(host, uid)
	sshPriv, sshPub, err := twssh.GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("generating SSH key pair: %w", err)
	}
	caCert, caKey, err := pki.GenerateCA(id)
	if err != nil {
		return nil, fmt.Errorf("generating CA: %w", err)
	}
	clientCert, clientKey, err := pki.IssueClientCert(caCert, caKey, id)
	if err != nil {
		return nil, fmt.Errorf("issuing client certificate: %w", err)
	}
	return &localServerIdentity{
		uuid: uid, hostname: host, serverID: id,
		sshPriv: sshPriv, sshPub: sshPub,
		caCert: caCert, caKey: caKey,
		client: clientCert, clientK: clientKey,
	}, nil
}

// defaultLocalServerContextName names the self-enrolled server context after
// the relay's first DNS label, mirroring the relay context convention
// ("relay-<label>") so the operator's two roles read as a pair.
func defaultLocalServerContextName(relayHost string) string {
	label := config.SanitizeName(strings.SplitN(relayHost, ".", 2)[0])
	if label == "" {
		return "server"
	}
	return "server-" + label
}

// buildLocalServerConfig assembles the new server context's config from the
// generated identity and the enrollment response — the same fields a remote
// server persists via the `tw join` enrollee flow.
// live is the relay profile's config, used to de-conflict daemon ports.
func buildLocalServerConfig(ident *localServerIdentity, resp *JoinResponse, live *config.Config) (*config.Config, error) {
	scfg := config.Default()
	scfg.Mode = "server"
	scfg.Xray.UUID = ident.uuid
	scfg.Xray.RelayHost = resp.RelayHost
	scfg.Xray.Path = resp.Path
	scfg.Server.RemotePort = resp.RemotePort
	scfg.Server.RelaySSHUser = resp.SSHUser

	// The signature must verify against the bundle's own identity before it is
	// stored — a signature that doesn't would brick the context (every command
	// would fail "mode signature invalid"). Degrade to legacy-unsigned instead.
	if resp.ModeSig != "" && resp.ModeIssuer != "" {
		if err := modeauth.Verify("server", strings.TrimSpace(string(ident.sshPub)), resp.ModeSig, resp.ModeIssuer); err != nil {
			slog.Warn("mode signature from local enroll does not verify; storing profile unsigned", "error", err)
		} else {
			scfg.ModeAuth = &config.ModeAuth{Sig: resp.ModeSig, Issuer: resp.ModeIssuer}
		}
	}

	// Both contexts live on this machine: if the relay profile holds the same
	// daemon ports the new context would get, the two daemons could never run
	// side by side (and the collision only surfaces as a buried bind error).
	if scfg.Server.DashboardPort == live.Server.DashboardPort {
		p, err := nextFreeLoopbackPort(live.Server.DashboardPort + 1)
		if err != nil {
			return nil, fmt.Errorf("picking dashboard port: %w", err)
		}
		scfg.Server.DashboardPort = p
	}
	if scfg.Server.APIPort == live.Server.APIPort {
		p, err := nextFreeLoopbackPort(live.Server.APIPort + 1)
		if err != nil {
			return nil, fmt.Errorf("picking API port: %w", err)
		}
		scfg.Server.APIPort = p
	}
	return scfg, nil
}

// nextFreeLoopbackPort returns the first loopback-bindable port >= start.
func nextFreeLoopbackPort(start int) (int, error) {
	for p := start; p < start+100 && p <= 65535; p++ {
		if loopbackPortFree(p) {
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free loopback port in [%d, %d)", start, start+100)
}

// sealLocalServerBundle zips the new server profile (config + identity) and
// cryptobox-seals it — the same shape unsealProfile/ImportContext consume.
// Unlike user bundles it carries ca.key: servers sign their users' certs.
func sealLocalServerBundle(ident *localServerIdentity, scfg *config.Config) ([]byte, error) {
	cfgYAML, err := yaml.Marshal(scfg)
	if err != nil {
		return nil, fmt.Errorf("marshaling server config: %w", err)
	}
	entries := []struct {
		name string
		data []byte
	}{
		{"config.yaml", cfgYAML},
		{"id_ed25519", ident.sshPriv},
		{"id_ed25519.pub", ident.sshPub},
		{"ca.crt", ident.caCert},
		{"ca.key", ident.caKey},
		{"client.crt", ident.client},
		{"client.key", ident.clientK},
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			return nil, fmt.Errorf("adding %s to bundle: %w", e.name, err)
		}
		if _, err := w.Write(e.data); err != nil {
			return nil, fmt.Errorf("writing %s to bundle: %w", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finalizing profile zip: %w", err)
	}
	sealed, err := cryptobox.Encrypt(buf.Bytes(), "")
	if err != nil {
		return nil, fmt.Errorf("sealing server context: %w", err)
	}
	return sealed, nil
}

// AddLocalServer enrolls THIS machine as a server tenant on its own relay and
// stores the result as a new, ready-to-use context — the single-operator
// alternative to the join-request/response file handshake. The whole exchange
// runs in-process: the server identity is generated in memory, enrolled via
// EnrollServer (which signs mode_auth with the relay key, so the context is
// born signed), and sealed straight into the context store. The live relay
// profile is never modified.
func (o *Ops) AddLocalServer(name string, progress ProgressFunc) (*LocalServerResult, error) {
	cfg := o.Config()
	if cfg.Xray.RelayHost == "" {
		return nil, fmt.Errorf("no relay configured — run 'tw relay create' first")
	}
	if name == "" {
		name = defaultLocalServerContextName(cfg.Xray.RelayHost)
	}
	// Refuse a taken name before touching the relay — nothing to roll back.
	idx, err := config.EnsureContextIndex()
	if err != nil {
		return nil, err
	}
	if _, exists := idx.Contexts[name]; exists {
		return nil, fmt.Errorf("%w: %s (pass a different context name)", ErrContextExists, name)
	}
	if !o.GetRelayStatus().Provisioned {
		return nil, fmt.Errorf("no relay is provisioned — run 'tw relay create' first")
	}

	ident, err := newLocalServerIdentity()
	if err != nil {
		return nil, err
	}
	req := &JoinRequest{
		Version:   1,
		ServerID:  ident.serverID,
		Hostname:  ident.hostname,
		UUID:      ident.uuid,
		RelayHost: cfg.Xray.RelayHost,
		CACertPEM: string(ident.caCert),
		SSHPubkey: strings.TrimSpace(string(ident.sshPub)),
	}
	resp, err := o.EnrollServer(req, progress)
	if err != nil {
		return nil, err
	}

	// The tenant is live on the relay from here on — any failure before the
	// context lands in the store must un-enroll it again, or it would linger
	// as an orphan no context can ever use.
	scfg, err := buildLocalServerConfig(ident, resp, cfg)
	var bundle []byte
	if err == nil {
		bundle, err = sealLocalServerBundle(ident, scfg)
	}
	if err == nil {
		_, err = o.ImportContext(bundle, name, false)
	}
	if err != nil {
		if uerr := o.UnenrollServer(ident.serverID, nil); uerr != nil {
			return nil, fmt.Errorf("%w — rollback failed too; remove the orphan tenant with 'tw relay un-enroll-server %s': %v", err, ident.serverID, uerr)
		}
		return nil, fmt.Errorf("storing server context (tenant %s rolled back): %w", ident.serverID, err)
	}
	return &LocalServerResult{
		ContextName:   name,
		ServerID:      ident.serverID,
		RemotePort:    resp.RemotePort,
		DashboardPort: scfg.Server.DashboardPort,
		APIPort:       scfg.Server.APIPort,
	}, nil
}
