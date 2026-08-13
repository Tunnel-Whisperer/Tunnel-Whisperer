package ops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

// TestCreateUserRollsBackUserDirOnLaterWriteFailure exercises the undo stack
// added to CreateUser: a failure in the LOCAL write path (step 3/4, after
// the user directory was already created) must roll the directory back,
// not leave an orphaned half-written user dir behind.
//
// Step 2 (addUUIDToRelay) is exercised too but fails fast and non-fatally
// here (no local SSH host key material in a fresh TW_CONFIG_DIR — see
// withRelaySSH), which is CreateUser's existing, unchanged behavior; this
// test only needs step 2 to run without hanging, not to succeed, so the
// undo path under test is the userDir one, not the relay-UUID one.
func TestCreateUserRollsBackUserDirOnLaterWriteFailure(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newTestOps(t)

	// validateCreateUser requires a configured relay + UUID.
	o.cfg.Xray.RelayHost = "relay.example.com"
	o.cfg.Xray.RelayPort = 443
	o.cfg.Xray.UUID = "11111111-1111-1111-1111-111111111111"

	// Force the LAST local write (authorized_keys, step 4) to fail: make its
	// path a directory instead of a file. os.ReadFile/os.WriteFile against a
	// directory fail with EISDIR regardless of privilege — root-proof, no
	// permission bits involved.
	if err := os.MkdirAll(config.AuthorizedKeysPath(), 0o755); err != nil {
		t.Fatal(err)
	}

	userDir := filepath.Join(config.UsersDir(), "alice")
	req := CreateUserRequest{Name: "alice", Mappings: []config.PortMapping{{ClientPort: 8080, ServerPort: 80}}}
	if err := o.CreateUser(context.Background(), req, nil); err == nil {
		t.Fatal("want an error (authorized_keys write must fail), got nil")
	}

	if _, statErr := os.Stat(userDir); !os.IsNotExist(statErr) {
		t.Fatalf("userDir was not rolled back after a later failure: stat err = %v", statErr)
	}
}
