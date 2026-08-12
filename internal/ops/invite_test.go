package ops

import (
	"strings"
	"testing"
	"time"
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
