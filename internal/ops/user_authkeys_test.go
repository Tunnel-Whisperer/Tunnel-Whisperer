package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

func seedUser(t *testing.T, name string, pub string, configYAML string) string {
	t.Helper()
	dir := filepath.Join(config.UsersDir(), name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if pub != "" {
		writeFile(t, filepath.Join(dir, "id_ed25519.pub"), pub)
	}
	if configYAML != "" {
		writeFile(t, filepath.Join(dir, "config.yaml"), configYAML)
	}
	return dir
}

func readAuthorizedKeys(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(config.AuthorizedKeysPath())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestAuthorizedKeysRewriteIsAtomicWithOwnerOnlyMode: authorized_keys is
// replaced via a temp file + rename (no leftovers), so it always ends up 0600
// — even when an older, looser file was there — with the right content.
func TestAuthorizedKeysRewriteIsAtomicWithOwnerOnlyMode(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.AuthorizedKeysPath(), []byte("ssh-ed25519 AAAAother other@tw"), 0o644); err != nil {
		t.Fatal(err)
	}
	pub := genPubKey(t)
	if err := appendAuthorizedKey([]byte(pub), "bob", []int{5432}, false); err != nil {
		t.Fatal(err)
	}
	want := "ssh-ed25519 AAAAother other@tw\n" + `permitopen="127.0.0.1:5432" ` + strings.TrimSpace(pub) + " bob@tw\n"
	if got := readAuthorizedKeys(t); got != want {
		t.Fatalf("authorized_keys =\n%q\nwant\n%q", got, want)
	}
	if err := removeAuthorizedKey([]byte(pub)); err != nil {
		t.Fatal(err)
	}
	if got := readAuthorizedKeys(t); got != "ssh-ed25519 AAAAother other@tw\n" {
		t.Fatalf("after remove: %q", got)
	}
	fi, err := os.Stat(config.AuthorizedKeysPath())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %04o, want 0600", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(config.Dir())
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

// TestSetUserSingleSessionRewritesUserLine: toggling single-session replaces
// the user's line in one rewrite and leaves everyone else's alone.
func TestSetUserSingleSessionRewritesUserLine(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	pub := genPubKey(t)
	seedUser(t, "bob", pub, "client:\n  tunnels:\n    - local_port: 1\n      remote_port: 5432\n")
	if err := appendAuthorizedKey([]byte(pub), "bob", []int{5432}, false); err != nil {
		t.Fatal(err)
	}
	other := genPubKey(t)
	if err := appendAuthorizedKey([]byte(other), "alice", []int{22}, false); err != nil {
		t.Fatal(err)
	}
	o := &Ops{cfg: &config.Config{}}
	if err := o.SetUserSingleSession("bob", true); err != nil {
		t.Fatal(err)
	}
	want := `permitopen="127.0.0.1:22" ` + strings.TrimSpace(other) + " alice@tw\n" +
		`single-session,permitopen="127.0.0.1:5432" ` + strings.TrimSpace(pub) + " bob@tw\n"
	if got := readAuthorizedKeys(t); got != want {
		t.Fatalf("authorized_keys =\n%q\nwant\n%q", got, want)
	}
}

// TestDeleteUserFailsWhenKeyRemovalFails: if authorized_keys cannot be
// rewritten, DeleteUser reports it and keeps the user (so it can be retried)
// instead of claiming success while the key still authenticates.
func TestDeleteUserFailsWhenKeyRemovalFails(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	dir := seedUser(t, "bob", genPubKey(t), "")
	if err := os.MkdirAll(config.AuthorizedKeysPath(), 0o700); err != nil { // unreadable as a file
		t.Fatal(err)
	}
	o := &Ops{cfg: &config.Config{}}
	if err := o.DeleteUser("bob"); err == nil {
		t.Fatal("DeleteUser succeeded although authorized_keys could not be updated")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("user dir removed although the key was not revoked: %v", err)
	}
}

// TestDeleteUserWithoutPubKeyRemovesByComment: a user whose id_ed25519.pub
// is missing still loses their authorized_keys line (matched by the
// "<name>@tw" comment), and only theirs.
func TestDeleteUserWithoutPubKeyRemovesByComment(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	bob, bobby := genPubKey(t), genPubKey(t)
	for name, pub := range map[string]string{"bob": bob, "bobby": bobby} {
		if err := appendAuthorizedKey([]byte(pub), name, []int{22}, false); err != nil {
			t.Fatal(err)
		}
	}
	dir := seedUser(t, "bob", "", "")
	o := &Ops{cfg: &config.Config{}}
	if err := o.DeleteUser("bob"); err != nil {
		t.Fatal(err)
	}
	got := readAuthorizedKeys(t)
	if strings.Contains(got, " bob@tw") {
		t.Errorf("bob's line survived: %q", got)
	}
	if !strings.Contains(got, strings.TrimSpace(bobby)+" bobby@tw") {
		t.Errorf("bobby's line removed: %q", got)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("user dir still present: %v", err)
	}
}

// TestListUsersLowercasesUUID: an uppercase UUID in a user's config is
// normalized when loaded, so ApplyUsers' strict canonical check accepts it.
func TestListUsersLowercasesUUID(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	seedUser(t, "bob", "", "xray:\n  uuid: A1B2C3D4-AAAA-BBBB-CCCC-DDDDEEEEFFFF\n")
	o := &Ops{cfg: &config.Config{}}
	users, err := o.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].UUID != "a1b2c3d4-aaaa-bbbb-cccc-ddddeeeeffff" || !isCanonicalUUID(users[0].UUID) {
		t.Fatalf("ListUsers = %+v, want the lowercase canonical uuid", users)
	}
}
