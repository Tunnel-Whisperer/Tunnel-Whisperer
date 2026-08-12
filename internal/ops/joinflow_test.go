package ops

import (
	"context"
	"strings"
	"testing"
)

// newTestOps builds an Ops against an empty profile under the caller's
// TW_CONFIG_DIR — the same construction addserver_test.go uses.
func newTestOps(t *testing.T) *Ops {
	t.Helper()
	o, err := New()
	if err != nil {
		t.Fatalf("ops.New: %v", err)
	}
	return o
}

func TestJoinRejectsBadCode(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newTestOps(t)
	_, err := o.Join(context.Background(), "relay.example.com", "garbage", "", JoinUI{}, nil)
	if err == nil || !strings.Contains(err.Error(), "invite code") {
		t.Fatalf("want code-shape error, got %v", err)
	}
}

func TestJoinServerGrantBuildsContext(t *testing.T) {
	// Exercises applyServerGrant directly: identity + JoinResponse in,
	// context stored, mode signed only when it verifies.
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newTestOps(t)
	ident, err := newLocalServerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	resp := &JoinResponse{Version: 1, ServerID: ident.serverID,
		RelayHost: "relay.example.com", Path: "/tw/" + ident.serverID,
		RemotePort: 20000, SSHUser: "tw"}
	grant, _ := resp.Encode()
	res, err := o.applyServerGrant(ident, grant, "myserver")
	if err != nil {
		t.Fatalf("applyServerGrant: %v", err)
	}
	if res.ContextName != "myserver" || res.Role != "server" {
		t.Fatalf("result: %+v", res)
	}
	names, err := o.ListContexts()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range names {
		if c.Name == "myserver" {
			found = true
		}
	}
	if !found {
		t.Fatal("context not stored")
	}
}
