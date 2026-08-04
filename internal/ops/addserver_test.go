package ops

import (
	"archive/zip"
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/cryptobox"
	"github.com/tunnelwhisperer/tw/internal/ops/modeauth"
	twssh "github.com/tunnelwhisperer/tw/internal/ssh"
)

func TestDefaultLocalServerContextName(t *testing.T) {
	cases := []struct{ relay, want string }{
		{"relay.example.com", "server-relay"},
		{"hds-t2.example.com", "server-hds-t2"},
		{"single", "server-single"},
		{"", "server"},
	}
	for _, c := range cases {
		if got := defaultLocalServerContextName(c.relay); got != c.want {
			t.Errorf("defaultLocalServerContextName(%q) = %q, want %q", c.relay, got, c.want)
		}
	}
}

func TestBuildLocalServerConfig(t *testing.T) {
	ident, err := newLocalServerIdentity()
	if err != nil {
		t.Fatalf("newLocalServerIdentity: %v", err)
	}

	// Sign the response like the relay does: over the identity's own pubkey.
	issuerPriv, _, err := twssh.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	sig, issuer, err := modeauth.Sign(issuerPriv, "server", strings.TrimSpace(string(ident.sshPub)))
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	resp := &JoinResponse{
		Version: 1, ServerID: ident.serverID, RelayHost: "relay.example.com",
		Path: "/tw/" + ident.serverID, RemotePort: 20003, SSHUser: "ubuntu",
		ModeSig: sig, ModeIssuer: issuer,
	}

	// Live relay profile holds the defaults → the new context must not.
	live := config.Default()
	scfg, err := buildLocalServerConfig(ident, resp, live)
	if err != nil {
		t.Fatalf("buildLocalServerConfig: %v", err)
	}
	if scfg.Mode != "server" || scfg.Xray.UUID != ident.uuid ||
		scfg.Xray.RelayHost != "relay.example.com" || scfg.Xray.Path != resp.Path ||
		scfg.Server.RemotePort != 20003 || scfg.Server.RelaySSHUser != "ubuntu" {
		t.Errorf("config fields wrong: %+v", scfg)
	}
	if scfg.ModeAuth == nil {
		t.Fatal("valid signature was not stored")
	}
	if err := modeauth.Verify("server", strings.TrimSpace(string(ident.sshPub)), scfg.ModeAuth.Sig, scfg.ModeAuth.Issuer); err != nil {
		t.Errorf("stored mode_auth does not verify: %v", err)
	}
	if scfg.Server.DashboardPort == live.Server.DashboardPort {
		t.Errorf("dashboard port %d collides with the relay profile's", scfg.Server.DashboardPort)
	}
	if scfg.Server.APIPort == live.Server.APIPort {
		t.Errorf("API port %d collides with the relay profile's", scfg.Server.APIPort)
	}

	// A signature over the WRONG identity must be dropped, not stored.
	badSig, badIssuer, err := modeauth.Sign(issuerPriv, "server", "ssh-ed25519 AAAA someoneelse")
	if err != nil {
		t.Fatal(err)
	}
	badResp := *resp
	badResp.ModeSig, badResp.ModeIssuer = badSig, badIssuer
	scfg2, err := buildLocalServerConfig(ident, &badResp, live)
	if err != nil {
		t.Fatalf("buildLocalServerConfig (bad sig): %v", err)
	}
	if scfg2.ModeAuth != nil {
		t.Error("non-verifying signature was stored; would brick the context")
	}

	// A relay profile with custom daemon ports → the defaults are kept.
	custom := config.Default()
	custom.Server.DashboardPort = 9999
	custom.Server.APIPort = 51000
	scfg3, err := buildLocalServerConfig(ident, resp, custom)
	if err != nil {
		t.Fatalf("buildLocalServerConfig (custom live ports): %v", err)
	}
	def := config.Default().Server
	if scfg3.Server.DashboardPort != def.DashboardPort || scfg3.Server.APIPort != def.APIPort {
		t.Errorf("ports changed although the relay profile holds custom ones: dashboard %d api %d",
			scfg3.Server.DashboardPort, scfg3.Server.APIPort)
	}
}

func TestSealLocalServerBundleRoundTrip(t *testing.T) {
	ident, err := newLocalServerIdentity()
	if err != nil {
		t.Fatalf("newLocalServerIdentity: %v", err)
	}
	resp := &JoinResponse{Version: 1, ServerID: ident.serverID, RelayHost: "relay.example.com",
		Path: "/tw/" + ident.serverID, RemotePort: 20001, SSHUser: "ubuntu"}
	scfg, err := buildLocalServerConfig(ident, resp, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealLocalServerBundle(ident, scfg)
	if err != nil {
		t.Fatalf("sealLocalServerBundle: %v", err)
	}

	plain, err := cryptobox.Decrypt(sealed, "")
	if err != nil {
		t.Fatalf("bundle does not decrypt with the empty passphrase: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(plain), int64(len(plain)))
	if err != nil {
		t.Fatalf("bundle is not a zip: %v", err)
	}
	got := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = b
	}
	for _, name := range []string{"config.yaml", "id_ed25519", "id_ed25519.pub", "ca.crt", "ca.key", "client.crt", "client.key"} {
		if len(got[name]) == 0 {
			t.Errorf("bundle entry %s missing or empty", name)
		}
	}

	var cfg config.Config
	if err := yaml.Unmarshal(got["config.yaml"], &cfg); err != nil {
		t.Fatalf("bundle config.yaml does not parse: %v", err)
	}
	if cfg.Mode != "server" || cfg.Xray.UUID != ident.uuid {
		t.Errorf("bundle config mode/uuid = %q/%q", cfg.Mode, cfg.Xray.UUID)
	}

	// The client cert's CN must be the server-id (the relay's Caddy subject
	// matcher keys on it) and the CA must actually be a CA.
	blk, _ := pem.Decode(got["client.crt"])
	if blk == nil {
		t.Fatal("client.crt is not PEM")
	}
	crt, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if crt.Subject.CommonName != ident.serverID {
		t.Errorf("client cert CN = %q, want %q", crt.Subject.CommonName, ident.serverID)
	}
	blk, _ = pem.Decode(got["ca.crt"])
	if blk == nil {
		t.Fatal("ca.crt is not PEM")
	}
	ca, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !ca.IsCA {
		t.Error("bundle CA cert is not a CA")
	}
}

func TestAddLocalServerGuards(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())

	// No relay configured at all.
	o, err := New()
	if err != nil {
		t.Fatalf("ops.New: %v", err)
	}
	if _, err := o.AddLocalServer("", nil); err == nil || !strings.Contains(err.Error(), "no relay configured") {
		t.Fatalf("expected 'no relay configured', got %v", err)
	}

	// Relay host set but the target context name is already taken: refused
	// with ErrContextExists BEFORE any enrollment side effects.
	writeFile(t, config.FilePath(), "mode: relay\nxray:\n  relay_host: relay.example.com\n")
	o2, err := New()
	if err != nil {
		t.Fatalf("ops.New: %v", err)
	}
	idx, err := config.EnsureContextIndex()
	if err != nil {
		t.Fatal(err)
	}
	idx.Contexts["server-relay"] = config.ContextMeta{Role: "server"}
	if err := config.SaveContextIndex(idx); err != nil {
		t.Fatal(err)
	}
	if _, err := o2.AddLocalServer("", nil); !errors.Is(err, ErrContextExists) {
		t.Fatalf("expected ErrContextExists for the default name, got %v", err)
	}
}
