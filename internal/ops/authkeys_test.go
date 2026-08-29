package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

func TestCanonicalAuthorizedKey(t *testing.T) {
	valid := genPubKey(t) // "ssh-ed25519 <base64>", no comment
	other := genPubKey(t)

	t.Run("valid single line", func(t *testing.T) {
		got, err := canonicalAuthorizedKey(valid)
		if err != nil || got != valid {
			t.Fatalf("canonicalAuthorizedKey(valid) = %q, %v; want %q, nil", got, err, valid)
		}
	})

	t.Run("trailing whitespace and newline tolerated", func(t *testing.T) {
		got, err := canonicalAuthorizedKey("  " + valid + "  \n")
		if err != nil || got != valid {
			t.Fatalf("got %q, %v; want %q, nil", got, err, valid)
		}
	})

	t.Run("comment is dropped", func(t *testing.T) {
		got, err := canonicalAuthorizedKey(valid + " alice@laptop")
		if err != nil || got != valid {
			t.Fatalf("comment not stripped: got %q, %v", got, err)
		}
	})

	t.Run("embedded newline (second key) rejected", func(t *testing.T) {
		if _, err := canonicalAuthorizedKey(valid + "\n" + other); err == nil {
			t.Fatal("a smuggled second key line must be rejected")
		}
	})

	t.Run("smuggled option rejected", func(t *testing.T) {
		// A leading option turns ParseAuthorizedKey's first token into an option
		// list; re-marshalling drops it, but we also refuse trailing data.
		if _, err := canonicalAuthorizedKey(`command="evil" ` + valid + "\n" + other); err == nil {
			t.Fatal("option-bearing multi-line input must be rejected")
		}
	})

	t.Run("garbage rejected", func(t *testing.T) {
		for _, bad := range []string{"", "   ", "not-a-key", "ssh-ed25519 AAAA"} {
			if _, err := canonicalAuthorizedKey(bad); err == nil {
				t.Errorf("canonicalAuthorizedKey(%q) should error", bad)
			}
		}
	})
}

// TestAppendAuthorizedKeyRejectsInjection is the SP-2/SP-4 regression at the
// server sink: a newline-bearing key must not add an unrestricted, permitopen-
// free line to authorized_keys.
func TestAppendAuthorizedKeyRejectsInjection(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(config.AuthorizedKeysPath()), 0o700); err != nil {
		t.Fatal(err)
	}

	good := genPubKey(t)
	evil := genPubKey(t)
	poisoned := good + "\n" + evil

	if err := appendAuthorizedKey([]byte(poisoned), "alice", []int{5432}, false); err == nil {
		t.Fatal("appendAuthorizedKey must reject a newline-bearing key")
	}
	// Nothing (or at least no unrestricted evil line) may have been written.
	if data, err := os.ReadFile(config.AuthorizedKeysPath()); err == nil {
		for _, l := range strings.Split(string(data), "\n") {
			if strings.Contains(l, evil) && !strings.Contains(l, "permitopen") {
				t.Fatalf("injected unrestricted line was written: %q", l)
			}
		}
	}

	// A clean key writes exactly one restricted line.
	if err := appendAuthorizedKey([]byte(good), "alice", []int{5432}, false); err != nil {
		t.Fatalf("clean key must be accepted: %v", err)
	}
	data, err := os.ReadFile(config.AuthorizedKeysPath())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want exactly one authorized_keys line, got %d:\n%s", len(lines), data)
	}
	if !strings.Contains(lines[0], `permitopen="127.0.0.1:5432"`) || !strings.Contains(lines[0], good) {
		t.Fatalf("line missing restriction or key: %q", lines[0])
	}
}

// TestDecodeJoinRequestRejectsInjection is the SP-1/SP-3 regression at the
// remote trust boundary: a poisoned join-request ssh_pubkey is refused, and a
// clean one is normalized to its canonical single-line form.
func TestDecodeJoinRequestRejectsInjection(t *testing.T) {
	good := genPubKey(t)
	evil := genPubKey(t)

	poisoned := &JoinRequest{
		Version: 1, ServerID: "srv-1", UUID: "u-1",
		CACertPEM: testCAPEM(t, "srv-1"), SSHPubkey: good + "\n" + evil,
	}
	b, _ := poisoned.Encode()
	if _, err := DecodeJoinRequest(b); err == nil {
		t.Fatal("a join request with a smuggled second key must be rejected")
	}

	clean := &JoinRequest{
		Version: 1, ServerID: "srv-1", UUID: "u-1",
		CACertPEM: testCAPEM(t, "srv-1"), SSHPubkey: good + " some-comment",
	}
	b, _ = clean.Encode()
	got, err := DecodeJoinRequest(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.SSHPubkey != good {
		t.Fatalf("ssh_pubkey not canonicalized: got %q, want %q", got.SSHPubkey, good)
	}
}
