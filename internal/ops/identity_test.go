package ops

import (
	"bytes"
	"os"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

func TestSanitizeHostname(t *testing.T) {
	cases := map[string]string{
		"Web-01.corp.local": "web-01-corp-local",
		"  My Host! ":        "my-host",
		"":                   "tw",
		"---":                "tw",
		"ALLCAPS":            "allcaps",
	}
	for in, want := range cases {
		if got := sanitizeHostname(in); got != want {
			t.Errorf("sanitizeHostname(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeriveServerID(t *testing.T) {
	if id := deriveServerID("Web-01", "a1b2c3d4-aaaa-bbbb-cccc-ddddeeeeffff"); id != "web-01-a1b2c3d4" {
		t.Errorf("deriveServerID = %q, want web-01-a1b2c3d4", id)
	}
	if id := deriveServerID("", "a1b2c3d4-xxxx"); id != "tw-a1b2c3d4" {
		t.Errorf("deriveServerID empty host = %q, want tw-a1b2c3d4", id)
	}
}

func TestFirstFreeFromBase(t *testing.T) {
	p, err := firstFreeFromBase(20000, []int{20000, 20001, 20003})
	if err != nil || p != 20002 {
		t.Fatalf("firstFreeFromBase = %d, %v; want 20002, nil", p, err)
	}
	if p, _ := firstFreeFromBase(20000, nil); p != 20000 {
		t.Errorf("empty used = %d, want 20000", p)
	}
}

const testUUID = "01234567-aaaa-bbbb-cccc-ddddeeeeffff"

func TestResolveServerIDStoredWins(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeIdentityFixture(t, "foo-01234567", "foo-01234567")
	cfg := config.Default()
	cfg.Xray.UUID = testUUID
	cfg.Xray.ServerID = "stored-01234567"
	if got := resolveServerID(cfg, config.ClientCertPath()); got != "stored-01234567" {
		t.Errorf("resolveServerID = %q, want stored-01234567", got)
	}
}

func TestResolveServerIDFromCertCN(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeIdentityFixture(t, "foo-01234567", "foo-01234567")
	cfg := config.Default()
	cfg.Xray.UUID = testUUID
	cfg.Xray.RelayHost = "relay.example.com"
	if got := resolveServerID(cfg, config.ClientCertPath()); got != "foo-01234567" {
		t.Errorf("resolveServerID = %q, want foo-01234567", got)
	}
}

func TestResolveServerIDLegacyRelayHostCN(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	// A bare-label relay host matches joinServerIDRe, so only the RelayHost
	// comparison marks it as the legacy CN.
	writeIdentityFixture(t, "relay", "relay")
	cfg := config.Default()
	cfg.Xray.UUID = testUUID
	cfg.Xray.RelayHost = "relay"
	host, _ := os.Hostname()
	if got, want := resolveServerID(cfg, config.ClientCertPath()), deriveServerID(host, testUUID); got != want {
		t.Errorf("resolveServerID = %q, want %q", got, want)
	}
}

func TestResolveServerIDNoCert(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	cfg := config.Default()
	cfg.Xray.UUID = testUUID
	host, _ := os.Hostname()
	if got, want := resolveServerID(cfg, config.ClientCertPath()), deriveServerID(host, testUUID); got != want {
		t.Errorf("resolveServerID = %q, want %q", got, want)
	}
}

func TestServerIDPersist(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	writeIdentityFixture(t, "foo-01234567", "foo-01234567")
	o, err := New()
	if err != nil {
		t.Fatal(err)
	}
	o.cfg.Mode = "server"
	o.cfg.Xray.UUID = testUUID

	id, err := o.serverID()
	if err != nil {
		t.Fatal(err)
	}
	if id != "foo-01234567" {
		t.Fatalf("serverID = %q, want foo-01234567", id)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Xray.ServerID != id {
		t.Fatalf("persisted xray.server_id = %q, want %q", loaded.Xray.ServerID, id)
	}

	before, err := os.ReadFile(config.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	again, err := o.serverID()
	if err != nil {
		t.Fatal(err)
	}
	if again != id {
		t.Errorf("second serverID = %q, want stable %q", again, id)
	}
	after, err := os.ReadFile(config.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("second serverID call rewrote config.yaml")
	}
}

func TestServerIDPersistClientMode(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o, err := New()
	if err != nil {
		t.Fatal(err)
	}
	o.cfg.Mode = "client"
	o.cfg.Xray.UUID = testUUID
	if err := config.Save(o.cfg); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(config.FilePath())
	if err != nil {
		t.Fatal(err)
	}

	id, err := o.serverID()
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Errorf("client-mode serverID = %q, want empty", id)
	}
	after, err := os.ReadFile(config.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("client-mode serverID rewrote config.yaml")
	}
}
