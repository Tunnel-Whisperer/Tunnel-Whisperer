package ops

import (
	"net"
	"os"
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

func newClientOpsForTest(t *testing.T) *Ops {
	t.Helper()
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, config.FilePath(), `mode: client
client:
  tunnels:
    - local_port: 8080
      remote_host: 127.0.0.1
      remote_port: 15432
    - local_port: 9090
      remote_host: 127.0.0.1
      remote_port: 15433
`)
	o, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestSetClientPortOverridePersists(t *testing.T) {
	o := newClientOpsForTest(t)
	if err := o.SetClientPortOverride(15432, 4000); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Client.PortOverrides[15432] != 4000 {
		t.Errorf("override not persisted: %v", cfg.Client.PortOverrides)
	}
	if cfg.Client.Tunnels[0].LocalPort != 8080 {
		t.Errorf("admin default mutated: %+v", cfg.Client.Tunnels[0])
	}
}

func TestSetClientPortOverrideUnknownServerPort(t *testing.T) {
	o := newClientOpsForTest(t)
	err := o.SetClientPortOverride(2222, 4000)
	if err == nil {
		t.Fatal("want error for unknown server port")
	}
	if !strings.Contains(err.Error(), "15432") || !strings.Contains(err.Error(), "15433") {
		t.Errorf("error should list valid server ports, got: %v", err)
	}
}

func TestSetClientPortOverrideRangeAndDuplicates(t *testing.T) {
	o := newClientOpsForTest(t)
	if err := o.SetClientPortOverride(15432, 0); err == nil {
		t.Error("want error for out-of-range local port 0")
	}
	if err := o.SetClientPortOverride(15432, 70000); err == nil {
		t.Error("want error for out-of-range local port 70000")
	}
	// 9090 is tunnel 15433's default → duplicate effective local port.
	if err := o.SetClientPortOverride(15432, 9090); err == nil {
		t.Error("want error for duplicate effective local port")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Client.PortOverrides) != 0 {
		t.Errorf("rejected override must not persist: %v", cfg.Client.PortOverrides)
	}
}

func TestClearClientPortOverride(t *testing.T) {
	o := newClientOpsForTest(t)
	if err := o.SetClientPortOverride(15432, 4000); err != nil {
		t.Fatal(err)
	}
	cleared, err := o.ClearClientPortOverride(15432)
	if err != nil || !cleared {
		t.Fatalf("clear existing: cleared=%v err=%v", cleared, err)
	}
	cleared, err = o.ClearClientPortOverride(15432)
	if err != nil || cleared {
		t.Fatalf("clear missing must be (false, nil): cleared=%v err=%v", cleared, err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Client.PortOverrides) != 0 {
		t.Errorf("override still on disk: %v", cfg.Client.PortOverrides)
	}
}

func TestPreflightBindReportsConflictActionably(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busy := ln.Addr().(*net.TCPAddr).Port

	tunnels := []config.Tunnel{{LocalPort: busy, RemoteHost: "127.0.0.1", RemotePort: 15432}}
	err = preflightBind("", tunnels)
	if err == nil {
		t.Fatal("want error for occupied port")
	}
	for _, want := range []string{"already in use", "tw client set-port 15432", "--map"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("preflight error missing %q: %v", want, err)
		}
	}

	free, err2 := freeLoopbackPort()
	if err2 != nil {
		t.Fatal(err2)
	}
	if err := preflightBind("", []config.Tunnel{{LocalPort: free, RemoteHost: "127.0.0.1", RemotePort: 15432}}); err != nil {
		t.Errorf("free port must pass preflight: %v", err)
	}
}
