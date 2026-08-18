package ops

import (
	"strings"
	"testing"
	"time"

	"github.com/tunnelwhisperer/tw/internal/config"
)

func TestInviteServerGuards(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o, err := New()
	if err != nil {
		t.Fatalf("ops.New: %v", err)
	}
	_, err = o.InviteServer(15*time.Minute, InviteUI{
		ShowCode:   func(string, time.Time) {},
		ConfirmSAS: func(string) bool { return false },
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "no relay configured") {
		t.Fatalf("want 'no relay configured' guard, got %v", err)
	}
}

func TestInviteUserGuards(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newTestOps(t)
	ui := InviteUI{ShowCode: func(string, time.Time) {}, ConfirmSAS: func(string) bool { return false }}
	err := o.InviteUser(CreateUserRequest{Name: "alice"}, time.Minute, ui, nil)
	if err == nil || !strings.Contains(err.Error(), "port mapping") {
		t.Fatalf("want mapping guard, got %v", err)
	}
	err = o.InviteUser(CreateUserRequest{Name: "bad name!", Mappings: []config.PortMapping{{ClientPort: 1, ServerPort: 2}}}, time.Minute, ui, nil)
	if err == nil {
		t.Fatal("want name-shape guard")
	}
}
