package cli

import (
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/ops"
)

// unenrollDetails is printed unconditionally — with --yes too, so scripted
// runs still log exactly what was removed.
func TestUnenrollDetails(t *testing.T) {
	d := unenrollDetails(&ops.RegisteredServer{
		ServerID: "web-1", RemotePort: 20001, EnrolledAt: "2026-07-01T10:30:00Z",
	})
	for _, want := range []string{"web-1", "20001", "2026-07-01T10:30"} {
		if !strings.Contains(d, want) {
			t.Errorf("details missing %q:\n%s", want, d)
		}
	}

	d = unenrollDetails(&ops.RegisteredServer{ServerID: "old-1", RemotePort: 20000})
	if !strings.Contains(d, "-") {
		t.Errorf("missing EnrolledAt must render as '-':\n%s", d)
	}
}
