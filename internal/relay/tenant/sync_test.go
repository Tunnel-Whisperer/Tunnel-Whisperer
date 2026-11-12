package tenant

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const testAdminKeys = `from="127.0.0.1" ssh-ed25519 AAAAadmin` + "\n"

func renderTestSync(t *testing.T) string {
	t.Helper()
	ak := `from="127.0.0.1",restrict,command="` + RequestPath + `",port-forwarding ssh-ed25519 AAAA` + "\n"
	script, err := RenderSync(&Admin{User: "ubuntu", AuthorizedKeys: testAdminKeys}, []Account{
		{ServerID: "srv-1", AuthorizedKeys: ak, Ports: []int{20000, 40000}},
		{ServerID: "srv-2", AuthorizedKeys: ak, Ports: []int{20001, 40001}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return script
}

// shellFunc returns the definition of the shell function name from script
// (from "name() {" to the first line that is just "}").
func shellFunc(t *testing.T, script, name string) string {
	t.Helper()
	start := strings.Index(script, "\n"+name+"() {")
	if start < 0 {
		t.Fatalf("sync script defines no %s()", name)
	}
	body := script[start+1:]
	end := strings.Index(body, "\n}\n")
	if end < 0 {
		t.Fatalf("%s() is not terminated", name)
	}
	return body[:end+3]
}

// runShell runs prelude + the named functions from script + main under
// sh -eu and returns combined output and the exit error.
func runShell(t *testing.T, script, prelude, main string, funcs ...string) (string, error) {
	t.Helper()
	var b strings.Builder
	b.WriteString("set -eu\n")
	b.WriteString(prelude)
	for _, f := range funcs {
		b.WriteString(shellFunc(t, script, f))
	}
	b.WriteString(main)
	out, err := exec.Command("sh", "-c", b.String()).CombinedOutput()
	return string(out), err
}

// TestRenderSyncAdminKeysLast (L3): the admin's authorized_keys is rewritten
// by the sync itself, after every tenant account was reconciled, so a sync
// that fails earlier leaves a pre-migration tenant's admin-account line in
// place instead of locking it out.
func TestRenderSyncAdminKeysLast(t *testing.T) {
	script := renderTestSync(t)
	call := "tw_admin_keys ubuntu " + base64.StdEncoding.EncodeToString([]byte(testAdminKeys)) + "\n"
	at := strings.Index(script, call)
	if at < 0 {
		t.Fatalf("sync script does not rewrite the admin keys (%q):\n%s", call, script)
	}
	for _, before := range []string{
		"tw_tenant_ensure " + UserName("srv-1") + " ",
		"tw_tenant_ensure " + UserName("srv-2") + " ",
		"*) tw_tenant_remove \"$u\" ;;",
		"if ! tw_check_tenant_match; then",
	} {
		i := strings.Index(script, before)
		if i < 0 || i > at {
			t.Errorf("%q must run before the admin keys are rewritten", before)
		}
	}
	if !strings.Contains(shellFunc(t, script, "tw_admin_keys"), `tw_kill_admin_sessions "$1" $$`) {
		t.Error("rewriting the admin keys must end the admin's other sessions")
	}
}

// fakePS is `ps -eo pid=,ppid=,args=` on a relay where the sync (pid 604)
// runs in an admin session, next to two other admin sessions (one OpenSSH
// >= 9.8 sshd-session, one older "sshd:") and sessions of other accounts.
const fakePS = `    1     0 /sbin/init
  500     1 sshd: /usr/sbin/sshd -D [listener] 0 of 10-100 startups
  600   500 sshd-session: ubuntu [priv]
  601   600 sshd-session: ubuntu@notty
  602   601 sh -c sudo sh -s
  603   602 sudo sh -s
  604   603 sh -s
  700   500 sshd-session: ubuntu [priv]
  701   700 sshd-session: ubuntu@notty
  800   500 sshd: ubuntu [priv]
  801   800 sshd: ubuntu@pts/0
  802   801 -bash
  900   500 sshd-session: ubuntu2 [priv]
  901   900 sshd-session: ubuntu2@notty
  950   500 sshd-session: twt-0123 [priv]
  951   950 sshd-session: twt-0123@notty
  960     1 bash -c sshd: ubuntu@notty
  970   500 sshd-session: ubuntu [net]
`

// TestKillAdminSessions (M2): every sshd session process of the admin
// account goes, except the chain the sync itself runs in; other accounts'
// sessions (including names with the admin's as prefix) stay.
func TestKillAdminSessions(t *testing.T) {
	script := renderTestSync(t)
	dir := t.TempDir()
	ps := filepath.Join(dir, "ps.txt")
	if err := os.WriteFile(ps, []byte(fakePS), 0o644); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "kill.log")
	prelude := `ps() { [ "$*" = "-eo pid=,ppid=,args=" ] || { echo "unexpected ps $*" >&2; exit 2; }; cat '` + ps + `'; }
kill() { if [ "$1" = -0 ]; then return 1; fi; echo "$@" >> '` + log + `'; }
sleep() { :; }
`
	out, err := runShell(t, script, prelude, "tw_kill_admin_sessions ubuntu 604\n", "tw_kill_admin_sessions")
	if err != nil {
		t.Fatalf("tw_kill_admin_sessions: %v\n%s", err, out)
	}
	data, _ := os.ReadFile(log)
	got := strings.Fields(string(data))
	sort.Strings(got)
	if want := []string{"700", "701", "800", "801"}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("killed %v, want %v (output %q)", got, want, out)
	}

	// Run from a process that is not in an admin session (e.g. cloud-init),
	// all admin sessions go.
	os.Remove(log)
	if out, err := runShell(t, script, prelude, "tw_kill_admin_sessions ubuntu 1\n", "tw_kill_admin_sessions"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	data, _ = os.ReadFile(log)
	got = strings.Fields(string(data))
	sort.Strings(got)
	if want := []string{"600", "601", "700", "701", "800", "801"}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("killed %v, want %v", got, want)
	}
}

// TestAdminKeysRewrite (M2): the admin's authorized_keys is replaced
// atomically, and only a change (tenant keys leaving it) ends the admin's
// other sessions; an in-sync file leaves them alone.
func TestAdminKeysRewrite(t *testing.T) {
	script := renderTestSync(t)
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	ak := filepath.Join(home, ".ssh", "authorized_keys")
	old := testAdminKeys + `restrict,port-forwarding,permitlisten="127.0.0.1:20000" ssh-ed25519 AAAAtenant` + "\n"
	if err := os.WriteFile(ak, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "calls.log")
	prelude := `getent() { echo "ubuntu:x:1000:1000::` + home + `:/bin/sh"; }
chown() { echo "chown $1" >> '` + log + `'; }
tw_kill_admin_sessions() { echo "kill-sessions $1 $2" >> '` + log + `'; }
`
	b64 := base64.StdEncoding.EncodeToString([]byte(testAdminKeys))
	main := "tw_admin_keys ubuntu " + b64 + "\n"
	if out, err := runShell(t, script, prelude, main, "tw_admin_keys"); err != nil {
		t.Fatalf("tw_admin_keys: %v\n%s", err, out)
	}
	if got, _ := os.ReadFile(ak); string(got) != testAdminKeys {
		t.Errorf("authorized_keys = %q, want the admin key only", got)
	}
	if st, err := os.Stat(ak); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("authorized_keys mode = %v (%v), want 0600", st.Mode().Perm(), err)
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "chown ubuntu:\n") || !strings.Contains(string(calls), "kill-sessions ubuntu ") {
		t.Errorf("changed keys must be chowned to the admin and end its other sessions:\n%s", calls)
	}
	if left, _ := filepath.Glob(ak + ".tw-*"); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}

	os.Remove(log)
	if out, err := runShell(t, script, prelude, main, "tw_admin_keys"); err != nil {
		t.Fatalf("tw_admin_keys (in sync): %v\n%s", err, out)
	}
	if calls, _ := os.ReadFile(log); len(calls) != 0 {
		t.Errorf("in-sync admin keys must not be rewritten or end sessions:\n%s", calls)
	}
}

// TestCheckTenantMatch (L5): the sync fails, and removes its drop-in, unless
// sshd's effective config for a twt-* account carries the Match block.
func TestCheckTenantMatch(t *testing.T) {
	script := renderTestSync(t)
	if !strings.Contains(script, "if ! tw_check_tenant_match; then\n\trm -f "+SSHDDropInPath+"\n") {
		t.Error("a failed Match check must remove the drop-in again")
	}
	fake := func(lines ...string) string {
		return "sshd() { printf '%s\\n' " + shellQuote(lines...) + "; }\n"
	}
	good := []string{"permittty no", "allowtcpforwarding remote", "allowstreamlocalforwarding no", "forcecommand " + RequestPath}
	if out, err := runShell(t, script, fake(good...), "tw_check_tenant_match\n", "tw_check_tenant_match"); err != nil {
		t.Errorf("effective Match block refused: %v\n%s", err, out)
	}
	bad := []string{"permittty no", "allowtcpforwarding yes", "allowstreamlocalforwarding no", "forcecommand " + RequestPath}
	if out, err := runShell(t, script, fake(bad...), "tw_check_tenant_match\n", "tw_check_tenant_match"); err == nil ||
		!strings.Contains(out, `lacks "allowtcpforwarding remote"`) {
		t.Errorf("overridden AllowTcpForwarding accepted: %v\n%s", err, out)
	}
	if out, err := runShell(t, script, "sshd() { echo 'bad option' >&2; return 255; }\n", "tw_check_tenant_match\n", "tw_check_tenant_match"); err == nil ||
		!strings.Contains(out, "sshd -T failed: bad option") {
		t.Errorf("failing sshd -T accepted: %v\n%s", err, out)
	}

	// Against the real sshd, when there is one.
	if _, err := os.Stat("/usr/sbin/sshd"); err != nil {
		t.Skip("no /usr/sbin/sshd to check the drop-in against")
	}
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("no ssh-keygen")
	}
	dir := t.TempDir()
	if out, err := exec.Command(keygen, "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(dir, "hk")).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "d", "98-tw-tenants.conf"), sshdDropIn, 0o644); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(dir, "probe_config")
	if err := os.WriteFile(probe, []byte("HostKey "+dir+"/hk\nUsePAM no\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/usr/sbin/sshd", "-T", "-f", probe, "-C", "user=x,host=x,addr=127.0.0.1").CombinedOutput(); err != nil {
		t.Skipf("sshd -T does not run here (%v): %s", err, out)
	}
	for name, tc := range map[string]struct {
		config string
		ok     bool
	}{
		"drop-in included":                   {"Include " + dir + "/d/*.conf\n", true},
		"drop-in directory not included":     {"", false},
		"earlier Match block wins":           {"Match User *\n\tAllowTcpForwarding yes\nInclude " + dir + "/d/*.conf\n", false},
		"global ForceCommand before drop-in": {"ForceCommand /bin/true\nInclude " + dir + "/d/*.conf\n", true},
	} {
		cfg := filepath.Join(dir, "sshd_config")
		if err := os.WriteFile(cfg, []byte("HostKey "+dir+"/hk\nUsePAM no\n"+tc.config), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := runShell(t, script, "sshd() { /usr/sbin/sshd -f '"+cfg+"' \"$@\"; }\n", "tw_check_tenant_match\n", "tw_check_tenant_match")
		if (err == nil) != tc.ok {
			t.Errorf("%s: check passed=%v, want %v\n%s", name, err == nil, tc.ok, out)
		}
	}
}

func shellQuote(args ...string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

// TestSyncSliceLimits (L1): each tenant account's login sessions run in a
// user slice capped in tasks and memory, so one tenant cannot exhaust the
// relay with connections or request processes.
func TestSyncSliceLimits(t *testing.T) {
	script := renderTestSync(t)
	ensure := shellFunc(t, script, "tw_tenant_ensure")
	if !strings.Contains(ensure, `tw_slice_limits "$uid"`) {
		t.Error("tw_tenant_ensure does not cap the tenant's user slice")
	}
	limits := shellFunc(t, script, "tw_slice_limits")
	for _, want := range []string{`/etc/systemd/system/user-$1.slice.d`, `"[Slice]" "TasksMax=32" "MemoryMax=256M"`} {
		if !strings.Contains(limits, want) {
			t.Errorf("tw_slice_limits lacks %s", want)
		}
	}
	if !strings.Contains(script, "\nsystemctl daemon-reload\ntw_admin_keys ") {
		t.Error("slice drop-ins must be loaded with a daemon-reload after the accounts are ensured")
	}
}
