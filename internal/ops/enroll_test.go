package ops

import (
	"strings"
	"testing"
)

// TestRenderRelayAuthorizedKeys is a regression test for the second-tenant
// enroll bug: key lines were appended without a trailing newline, so the
// second server's line glued onto the first server's, corrupting both entries
// and making the relay sshd reject both keys. The file is now fully rewritten
// from the tenant list, every line newline-terminated.
func TestRenderRelayAuthorizedKeys(t *testing.T) {
	servers := []RegisteredServer{
		{RemotePort: 20000, SSHPubkey: "ssh-ed25519 AAAAkey1 s1@tw\n"}, // trailing newline must be trimmed
		{RemotePort: 20001, SSHPubkey: "ssh-ed25519 AAAAkey2 s2@tw"},
	}
	out := renderRelayAuthorizedKeys("ssh-ed25519 AAAAadmin admin@tw\n", servers)

	if !strings.HasSuffix(out, "\n") {
		t.Error("authorized_keys content must end with a newline")
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines (admin + 2 servers), got %d:\n%s", len(lines), out)
	}
	if lines[0] != `from="127.0.0.1" ssh-ed25519 AAAAadmin admin@tw` {
		t.Errorf("admin line = %q", lines[0])
	}
	for i, port := range []string{"20000", "20001"} {
		l := lines[i+1]
		for _, want := range []string{
			`from="127.0.0.1"`, "restrict", "port-forwarding",
			`permitopen="127.0.0.1:1"`, `permitlisten="127.0.0.1:` + port + `"`,
		} {
			if !strings.Contains(l, want) {
				t.Errorf("server line %d missing %q: %q", i+1, want, l)
			}
		}
		if strings.Contains(l, "\n") {
			t.Errorf("server line %d contains embedded newline: %q", i+1, l)
		}
	}
}
