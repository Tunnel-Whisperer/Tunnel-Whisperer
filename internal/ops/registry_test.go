package ops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

// TestAddServerRejectsAdminOwnID: the admin's own identity must never land
// in the registry — un-enrolling it would tear the admin's own tenant out
// of the relay. Unreachable in practice (the admin doesn't join itself),
// but cheap to refuse outright.
func TestAddServerRejectsAdminOwnID(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o, err := New()
	if err != nil {
		t.Fatal(err)
	}
	o.cfg.Xray.UUID = "a1b2c3d4-aaaa-bbbb-cccc-ddddeeeeffff"
	host, _ := os.Hostname()
	adminID := deriveServerID(host, o.cfg.Xray.UUID)

	_, err = o.AddServer(&JoinRequest{Version: 1, ServerID: adminID, UUID: "00000000-0000-4000-8000-000000000001",
		Hostname: "x", SSHPubkey: "ssh-ed25519 AAAA x@tw"})
	if err == nil {
		t.Fatal("enrolling the relay's own server-id must be refused")
	}
	if !strings.Contains(err.Error(), "own") {
		t.Errorf("error should explain it is the relay's own identity, got: %v", err)
	}

	// A normal id is unaffected.
	if _, err := o.AddServer(&JoinRequest{Version: 1, ServerID: "srv-1", UUID: "00000000-0000-4000-8000-000000000002",
		Hostname: "srv", CACertPEM: testCAPEM(t, "srv-1"), SSHPubkey: genPubKey(t)}); err != nil {
		t.Fatalf("normal enroll must still work: %v", err)
	}
}

func TestAddAndListServers(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	o, _ := New()
	a, err := o.AddServer(&JoinRequest{Version: 1, ServerID: "a-1", UUID: "00000000-0000-4000-8000-00000000000a", CACertPEM: testCAPEM(t, "a-1"), SSHPubkey: genPubKey(t)})
	if err != nil {
		t.Fatal(err)
	}
	if a.RemotePort != 20000 {
		t.Errorf("first port = %d, want 20000", a.RemotePort)
	}
	b, err := o.AddServer(&JoinRequest{Version: 1, ServerID: "b-2", UUID: "00000000-0000-4000-8000-00000000000b", CACertPEM: testCAPEM(t, "b-2"), SSHPubkey: genPubKey(t)})
	if err != nil {
		t.Fatal(err)
	}
	if b.RemotePort != 20001 {
		t.Errorf("second port = %d, want 20001", b.RemotePort)
	}
	if _, err := o.AddServer(&JoinRequest{Version: 1, ServerID: "a-1", UUID: "00000000-0000-4000-8000-0000000000a2", CACertPEM: testCAPEM(t, "a-1"), SSHPubkey: genPubKey(t)}); err == nil {
		t.Error("duplicate server-id should be rejected")
	}
	list, _ := o.ListServers()
	if len(list) != 2 {
		t.Fatalf("want 2 servers, got %d", len(list))
	}
	for _, s := range list {
		if s.EnrolledAt == "" {
			t.Errorf("server %s missing EnrolledAt stamp", s.ServerID)
		}
	}
}

func TestParseListeningPorts(t *testing.T) {
	// Realistic concatenated /proc/net/tcp + /proc/net/tcp6 content: hex
	// local ports, state 0A = LISTEN. Includes an ESTABLISHED (01) row and
	// headers, both of which must be ignored.
	out := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:4E20 00000000:0000 0A 00000000:00000000 00:00000000 00000000   112        0 12345 1 0000000000000000 100 0 0 10 0
   1: 0100007F:2765 00000000:0000 0A 00000000:00000000 00:00000000 00000000   112        0 12346 1 0000000000000000 100 0 0 10 0
   2: 0100007F:4E21 0100007F:0016 01 00000000:00000000 00:00000000 00000000     0        0 12347 1 0000000000000000 100 0 0 10 0
  sl  local_address                         rem_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:01BB 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12348 1 0000000000000000 100 0 0 10 0
`
	ports := parseListeningPorts(out)
	for _, want := range []int{20000, 10085, 443} { // 0x4E20, 0x2765, 0x01BB
		if !ports[want] {
			t.Errorf("port %d not detected in:\n%s", want, out)
		}
	}
	if ports[20001] { // 0x4E21 is ESTABLISHED, not LISTEN
		t.Error("port 20001 (non-LISTEN row) falsely detected")
	}
}

// TestListServersSkipsInvalidEntries: registry entries are validated at load
// time like a join request, so an entry planted through a bundle never
// reaches the relay renders — but LookupServer still finds it for un-enroll.
func TestListServersSkipsInvalidEntries(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o, _ := New()
	if _, err := o.AddServer(&JoinRequest{Version: 1, ServerID: "good-1", UUID: "00000000-0000-4000-8000-0000000000c1",
		CACertPEM: testCAPEM(t, "good-1"), SSHPubkey: genPubKey(t)}); err != nil {
		t.Fatal(err)
	}
	key := genPubKey(t)
	write := func(file string, s RegisteredServer) {
		data, _ := json.Marshal(s)
		writeFile(t, filepath.Join(RegistryDir(), file), string(data))
	}
	valid := func(id string) RegisteredServer {
		return RegisteredServer{ServerID: id, UUID: "00000000-0000-4000-8000-0000000000d1",
			RemotePort: 20100, CACertPEM: testCAPEM(t, id), SSHPubkey: key}
	}
	bad := map[string]RegisteredServer{}
	s := valid("bad-uuid")
	s.UUID = "not-a-uuid"
	bad["bad-uuid"] = s
	s = valid("two-certs")
	s.CACertPEM += testCAPEM(t, "good-1")
	bad["two-certs"] = s
	s = valid("cn-mismatch")
	s.CACertPEM = testCAPEM(t, "good-1")
	bad["cn-mismatch"] = s
	s = valid("multi-key")
	s.SSHPubkey = key + "\n" + genPubKey(t)
	bad["multi-key"] = s
	s = valid("bad-port")
	s.RemotePort = 70000
	bad["bad-port"] = s
	for id, s := range bad {
		write(id+".json", s)
	}
	write("renamed.json", valid("other-id"))
	writeFile(t, filepath.Join(RegistryDir(), "Bad_ID.json"), `{"server_id":"Bad_ID"}`)

	list, err := o.ListServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ServerID != "good-1" {
		t.Fatalf("ListServers = %+v, want only good-1", list)
	}
	got, err := o.LookupServer("bad-uuid")
	if err != nil || got == nil || got.ServerID != "bad-uuid" {
		t.Fatalf("LookupServer(bad-uuid) = %+v, %v; want the invalid entry", got, err)
	}
	if got, err := o.LookupServer("../x"); got != nil || err != nil {
		t.Fatalf("LookupServer(../x) = %+v, %v; want nil, nil", got, err)
	}
}
