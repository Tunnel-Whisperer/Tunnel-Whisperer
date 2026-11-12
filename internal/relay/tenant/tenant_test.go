package tenant

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestUserName(t *testing.T) {
	got := UserName("srv-1")
	if !regexp.MustCompile(`^twt-[0-9a-f]{24}$`).MatchString(got) {
		t.Fatalf("UserName = %q, want twt-<24 hex>", got)
	}
	if got != UserName("srv-1") {
		t.Fatal("UserName is not deterministic")
	}
	if UserName("srv-1") == UserName("srv-2") {
		t.Fatal("distinct server ids must map to distinct accounts")
	}
	if len(got) > 32 {
		t.Fatalf("%q is longer than a Linux user name may be", got)
	}
	// tw-tenant-handler re-derives the account from the server id; the two
	// rules must never drift apart.
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available to cross-check the handler's rule")
	}
	out, err := exec.Command(py, "-I", "-c",
		`import hashlib,sys; print("twt-" + hashlib.sha256(sys.argv[1].encode()).hexdigest()[:24])`, "srv-1").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != got {
		t.Fatalf("handler rule gives %q, Go gives %q", strings.TrimSpace(string(out)), got)
	}
}

func TestRenderSync(t *testing.T) {
	ak := `from="127.0.0.1",restrict,command="` + RequestPath + `",port-forwarding ssh-ed25519 AAAA` + "\n"
	script, err := RenderSync(nil, []Account{
		{ServerID: "srv-1", AuthorizedKeys: ak, Ports: []int{20000, 40000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	user := UserName("srv-1")
	want := "tw_tenant_ensure " + user + " srv-1 " + base64.StdEncoding.EncodeToString([]byte(ak)) + " " +
		base64.StdEncoding.EncodeToString([]byte("srv-1\n")) + " 20000 40000\n"
	if !strings.Contains(script, want) {
		t.Errorf("script lacks %q:\n%s", want, script)
	}
	if !strings.Contains(script, `tw_keep=" `+user+` "`) {
		t.Errorf("listed account must be kept, not removed:\n%s", script)
	}
	for _, f := range Files() {
		if !strings.Contains(script, "tw_put "+f.Path+" "+f.Mode+" "+f.B64()+"\n") {
			t.Errorf("script does not install %s", f.Path)
		}
	}
	c := exec.Command("sh", "-n")
	c.Stdin = strings.NewReader(script)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("script does not parse as shell: %v\n%s", err, out)
	}

	if strings.Contains(script, "tw_admin_keys ") && !strings.Contains(script, "tw_admin_keys() {") {
		t.Error("no admin given: the admin's authorized_keys must be left alone")
	}

	empty, err := RenderSync(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(empty, `tw_keep=" "`) || strings.Contains(empty, "tw_tenant_ensure twt-") {
		t.Errorf("no accounts must keep none:\n%s", empty)
	}
}

func TestRenderSyncRejects(t *testing.T) {
	ak := "ssh-ed25519 AAAA\n"
	for name, a := range map[string]Account{
		"server id with shell metachars": {ServerID: "a;reboot", AuthorizedKeys: ak},
		"server id uppercase":            {ServerID: "Srv", AuthorizedKeys: ak},
		"server id too long":             {ServerID: strings.Repeat("a", 65), AuthorizedKeys: ak},
		"two key lines":                  {ServerID: "s", AuthorizedKeys: ak + ak},
		"unterminated key line":          {ServerID: "s", AuthorizedKeys: "ssh-ed25519 AAAA"},
		"bad port":                       {ServerID: "s", AuthorizedKeys: ak, Ports: []int{70000}},
	} {
		if _, err := RenderSync(nil, []Account{a}); err == nil {
			t.Errorf("%s: RenderSync accepted %+v", name, a)
		}
	}
	if _, err := RenderSync(nil, []Account{{ServerID: "s", AuthorizedKeys: ak}, {ServerID: "s", AuthorizedKeys: ak}}); err == nil {
		t.Error("duplicate server id accepted")
	}
	for name, a := range map[string]Admin{
		"admin name with shell metachars": {User: "ubuntu;reboot", AuthorizedKeys: ak},
		"admin name with path":            {User: "../root", AuthorizedKeys: ak},
		"empty admin keys":                {User: "ubuntu"},
		"unterminated admin keys":         {User: "ubuntu", AuthorizedKeys: "ssh-ed25519 AAAA"},
	} {
		if _, err := RenderSync(&a, nil); err == nil {
			t.Errorf("%s: RenderSync accepted admin %+v", name, a)
		}
	}
}

func TestForcedCommandEverywhere(t *testing.T) {
	if !strings.Contains(string(sshdDropIn), "Match User twt-*\n\tForceCommand "+RequestPath+"\n") {
		t.Errorf("sshd drop-in must force %s for tenant accounts:\n%s", RequestPath, sshdDropIn)
	}
	for _, f := range []string{"ListenStream=/run/tw/tenant/%i.sock", "SocketUser=%i", "SocketMode=0600", "Accept=yes"} {
		if !strings.Contains(string(socketUnit), f+"\n") {
			t.Errorf("socket unit lacks %s", f)
		}
	}
	for _, f := range []string{"ExecStart=/usr/bin/python3 -I " + HandlerPath, "StandardInput=socket", "ReadWritePaths=/usr/local/etc/xray", "ProtectSystem=strict", "NoNewPrivileges=yes"} {
		if !strings.Contains(string(serviceUnit), f+"\n") {
			t.Errorf("service unit lacks %s", f)
		}
	}
}

// TestHandlerUnitHardening: the root handler keeps only the capabilities
// its config rewrite needs, reaches only loopback, and is bounded in time,
// tasks and memory.
func TestHandlerUnitHardening(t *testing.T) {
	for _, f := range []string{
		"CapabilityBoundingSet=CAP_CHOWN CAP_DAC_OVERRIDE",
		"SystemCallFilter=@system-service",
		"SystemCallArchitectures=native",
		"IPAddressDeny=any",
		"IPAddressAllow=localhost",
		"ProtectProc=invisible",
		"RestrictSUIDSGID=yes",
		"LockPersonality=yes",
		"ProtectClock=yes",
		"ProtectKernelLogs=yes",
		"ProtectHostname=yes",
		"RestrictRealtime=yes",
		"UMask=0077",
		"RuntimeMaxSec=60",
		"TasksMax=32",
		"MemoryMax=256M",
	} {
		if !strings.Contains(string(serviceUnit), "\n"+f+"\n") {
			t.Errorf("service unit lacks %s", f)
		}
	}
	if strings.Contains(string(serviceUnit), "systemctl") || strings.Contains(string(handlerScript), "systemctl") {
		t.Error("the tenant path must never restart xray")
	}
	analyze, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Skip("systemd-analyze not available")
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{"tw-tenant@.service": serviceUnit, "tw-tenant@.socket": socketUnit} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := exec.Command(analyze, "verify", filepath.Join(dir, "tw-tenant@.socket"), filepath.Join(dir, "tw-tenant@.service")).CombinedOutput()
	if err != nil || len(strings.TrimSpace(string(out))) != 0 {
		t.Errorf("systemd-analyze verify: %v\n%s", err, out)
	}
}

// TestSSHDDropInHardening: the tenant Match block allows public keys only,
// no gateway ports and no local forwarding, and one request session per
// connection besides the tunnel.
func TestSSHDDropInHardening(t *testing.T) {
	for _, f := range []string{"AuthenticationMethods publickey", "KbdInteractiveAuthentication no", "AllowTcpForwarding remote",
		"GatewayPorts no", "PermitOpen none", "MaxSessions 2"} {
		if !strings.Contains(string(sshdDropIn), "\n\t"+f+"\n") {
			t.Errorf("sshd drop-in lacks %s", f)
		}
	}
}
