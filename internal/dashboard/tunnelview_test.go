package dashboard

import (
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

func TestBuildTunnelViews(t *testing.T) {
	c := config.ClientConfig{
		Tunnels: []config.Tunnel{
			{LocalPort: 8080, RemoteHost: "127.0.0.1", RemotePort: 15432},
			{LocalPort: 9090, RemoteHost: "127.0.0.1", RemotePort: 15433},
		},
		PortOverrides: map[int]int{15432: 4000},
	}
	views := buildTunnelViews(c)
	if len(views) != 2 {
		t.Fatalf("want 2 views, got %d", len(views))
	}
	want0 := tunnelView{ServerPort: 15432, RemoteHost: "127.0.0.1", DefaultPort: 8080, OverridePort: 4000, EffectivePort: 4000}
	if views[0] != want0 {
		t.Errorf("overridden view: got %+v, want %+v", views[0], want0)
	}
	want1 := tunnelView{ServerPort: 15433, RemoteHost: "127.0.0.1", DefaultPort: 9090, OverridePort: 0, EffectivePort: 9090}
	if views[1] != want1 {
		t.Errorf("plain view: got %+v, want %+v", views[1], want1)
	}
}

func TestBuildTunnelViewsEmpty(t *testing.T) {
	if views := buildTunnelViews(config.ClientConfig{}); len(views) != 0 {
		t.Errorf("want empty, got %+v", views)
	}
}
