package ops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/enroll"
	"github.com/tunnelwhisperer/tw/internal/pki"
)

// newExpiryOps builds an Ops with a server CA under a temp TW_CONFIG_DIR and
// stubs the relay calls grantClient makes.
func newExpiryOps(t *testing.T) *Ops {
	t.Helper()
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	caCert, caKey, err := pki.GenerateCA("srv-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.CACertPath(), caCert, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.CAKeyPath(), caKey, 0o600); err != nil {
		t.Fatal(err)
	}
	oldAdd, oldRemove := relayAddUUID, relayRemoveUUID
	relayAddUUID = func(*config.Config, string) error { return nil }
	relayRemoveUUID = func(*config.Config, string) error { return nil }
	t.Cleanup(func() { relayAddUUID, relayRemoveUUID = oldAdd, oldRemove })
	return &Ops{cfg: &config.Config{Xray: config.XrayConfig{ServerID: "srv-1"}}}
}

func grantForTest(t *testing.T, o *Ops, name string, exp time.Time) []byte {
	t.Helper()
	var c clientMaterial
	if _, err := c.makeOffer(name); err != nil {
		t.Fatal(err)
	}
	off := enroll.ClientOffer{Username: name, UUID: c.uuid, SSHPubkey: string(c.sshPub), CSRPEM: string(c.csrPEM)}
	req := CreateUserRequest{
		Name:      name,
		Mappings:  []config.PortMapping{{ClientPort: 18080, ServerPort: 80}},
		ExpiresAt: exp,
	}
	out, err := o.grantClient(*o.cfg, req, &off)
	if err != nil {
		t.Fatalf("grantClient(%s): %v", name, err)
	}
	return out
}

func TestGrantClientStoresCertWithExpiry(t *testing.T) {
	o := newExpiryOps(t)
	want := time.Now().Add(48 * time.Hour).Truncate(time.Second).UTC()
	out := grantForTest(t, o, "bob", want)

	path := filepath.Join(config.UsersDir(), "bob", "client.crt")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o644 {
		t.Fatalf("client.crt mode = %v, want 0644", st.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pki.CertNotAfter(data)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Fatalf("NotAfter = %v, want %v", got, want)
	}
	var g enroll.ClientGrant
	if err := json.Unmarshal(out, &g); err != nil {
		t.Fatal(err)
	}
	if g.ClientCertPEM != string(data) {
		t.Fatal("grant ClientCertPEM differs from stored client.crt")
	}
}

func TestListUsersReportsExpiry(t *testing.T) {
	o := newExpiryOps(t)
	want := time.Now().Add(48 * time.Hour).Truncate(time.Second).UTC()
	grantForTest(t, o, "bob", want)
	grantForTest(t, o, "carol", time.Now().Add(-30*time.Minute))
	if err := os.MkdirAll(filepath.Join(config.UsersDir(), "old"), 0o700); err != nil {
		t.Fatal(err)
	}

	users, err := o.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]UserInfo{}
	for _, u := range users {
		by[u.Name] = u
	}
	if b := by["bob"]; b.ExpiresAt != want.Format(time.RFC3339) || b.Expired {
		t.Fatalf("bob = %+v", b)
	}
	if c := by["carol"]; c.ExpiresAt == "" || !c.Expired {
		t.Fatalf("carol = %+v", c)
	}
	if u := by["old"]; u.ExpiresAt != "" || u.Expired {
		t.Fatalf("old = %+v", u)
	}
}

func TestListUsersTolerantOfBadCert(t *testing.T) {
	o := newExpiryOps(t)
	dir := filepath.Join(config.UsersDir(), "dave")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "client.crt"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	users, err := o.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Name != "dave" || users[0].ExpiresAt != "" || users[0].Expired {
		t.Fatalf("users = %+v", users)
	}
}

func TestGrantClientDefaultExpiryIsFiveYears(t *testing.T) {
	o := newExpiryOps(t)
	grantForTest(t, o, "erin", time.Time{})
	users, err := o.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Fatalf("users = %+v", users)
	}
	got, err := time.Parse(time.RFC3339, users[0].ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Now().Add(5 * 365 * 24 * time.Hour)
	if d := got.Sub(want); d < -time.Minute || d > time.Minute {
		t.Fatalf("ExpiresAt = %v, want within 1m of %v", got, want)
	}
	if users[0].Expired {
		t.Fatal("default-expiry user reported expired")
	}
}
