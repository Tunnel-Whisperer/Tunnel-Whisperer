package cli

import (
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

func TestFormatPortOverrides(t *testing.T) {
	c := config.ClientConfig{
		Tunnels: []config.Tunnel{
			{LocalPort: 8080, RemoteHost: "127.0.0.1", RemotePort: 15432},
			{LocalPort: 9090, RemoteHost: "127.0.0.1", RemotePort: 15433},
		},
		PortOverrides: map[int]int{15432: 4000},
	}
	out := formatPortOverrides(c)
	for _, want := range []string{"SERVER PORT", "15432", "8080", "4000", "15433", "9090", "-"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing missing %q:\n%s", want, out)
		}
	}
	// The overridden row's effective port is the override; the plain row's
	// effective port is its default.
	if !strings.Contains(out, "4000") || strings.Count(out, "9090") < 2 {
		t.Errorf("effective column wrong:\n%s", out)
	}
}
