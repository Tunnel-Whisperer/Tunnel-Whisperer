package cli

import (
	"reflect"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

func TestContextCandidates(t *testing.T) {
	got := contextCandidates([]ops.ContextInfo{
		{Name: "alice", ID: "a1b2c3d4", Role: "client", User: "alice", Relay: "relay.example"},
		{Name: "hq", ID: "deadbeef", Role: "relay", Relay: "relay.example"},
		{Name: "scratch"}, // empty ID → name entry only, no description
	})
	want := []string{
		"alice\tclient alice@relay.example",
		"a1b2c3d4\tid of alice",
		"hq\trelay@relay.example",
		"deadbeef\tid of hq",
		"scratch",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("contextCandidates =\n%q\nwant\n%q", got, want)
	}
}

func TestUserCandidates(t *testing.T) {
	users := []ops.UserInfo{
		{Name: "alice", Tunnels: []config.Tunnel{{}}, Active: true},
		{Name: "bob"},
	}
	got := userCandidates(users, nil)
	want := []string{
		"alice\t1 tunnel(s), applied",
		"bob\t0 tunnel(s), not applied",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("userCandidates =\n%q\nwant\n%q", got, want)
	}
	// apply's multi-arg form excludes names already on the command line.
	if got := userCandidates(users, []string{"alice"}); !reflect.DeepEqual(got, want[1:]) {
		t.Errorf("userCandidates(exclude alice) = %q, want %q", got, want[1:])
	}
}

func TestServerCandidates(t *testing.T) {
	got := serverCandidates([]ops.RegisteredServer{
		{ServerID: "web-01-a1b2c3d4", RemotePort: 20000, EnrolledAt: "2026-07-01T10:30:00Z"},
		{ServerID: "old-1", RemotePort: 20001}, // pre-stamp entry: no enrolled date
	})
	want := []string{
		"web-01-a1b2c3d4\tport 20000, enrolled 2026-07-01T10:30:00Z",
		"old-1\tport 20001",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("serverCandidates =\n%q\nwant\n%q", got, want)
	}
}

func TestAppCandidates(t *testing.T) {
	got := appCandidates([]config.Application{
		{Name: "web", Mappings: []config.PortMapping{{ClientPort: 8080, ServerPort: 80}}},
	})
	want := []string{"web\t1 mapping(s)"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("appCandidates = %q, want %q", got, want)
	}
}
