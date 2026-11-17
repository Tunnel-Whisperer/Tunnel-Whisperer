package tenant

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

// handlerHarness loads tw-tenant-handler as a module, points its paths at a
// scratch tree, replaces the uid -> account lookup (the test runs as one
// real uid) and runs its main() with the accepted connection as stdin, the
// way systemd's Accept=yes socket runs it.
const handlerHarness = `
import importlib.machinery, importlib.util, json, sys
loader = importlib.machinery.SourceFileLoader("tw_tenant_handler", sys.argv[1])
spec = importlib.util.spec_from_loader(loader.name, loader)
h = importlib.util.module_from_spec(spec)
loader.exec_module(h)
o = json.loads(sys.argv[2])
for k, v in o["consts"].items():
    if not hasattr(h, k):
        raise SystemExit("unknown handler constant " + k)
    setattr(h, k, v)
peers = {int(k): v for k, v in o["peers"].items()}
h.user_for_uid = lambda uid: peers[uid]
sys.exit(h.main())
`

// fakeXray logs each call and answers like a successful `xray api adu|rmu`
// (see xray-core main/commands/all/api/inbound_user_{add,remove}.go); with
// $DIR/xray-fail present it fails like an unreachable API, and with
// $DIR/adu-out or $DIR/rmu-out present it prints that file instead.
const fakeXray = `#!/bin/sh
d=$(dirname "$0")
{ echo "xray $*"; [ "$3" = "-s" ] && [ "$5" = "stdin:" ] && cat; echo; } >> "$d/calls.log"
[ -e "$d/xray-fail" ] && { echo "failed to dial 127.0.0.1:10085" >&2; exit 1; }
[ -e "$d/$2-out" ] && { cat "$d/$2-out"; exit 0; }
case "$2" in
adu) printf 'processing inbound: vless-in-x\nadd user: u\nresult: ok\nAdded 1 user(s) in total.\n' ;;
rmu) printf 'remove user: u\nRemoved 1 user(s) in total.\n' ;;
esac
`

const (
	alphaUUID = "aaaaaaaa-0000-4000-8000-000000000001"
	betaUUID  = "bbbbbbbb-0000-4000-8000-000000000002"
	betaUser1 = "bbbbbbbb-1111-4000-8000-000000000003"
	newUser   = "cccccccc-2222-4000-8000-000000000004"
)

type handlerEnv struct {
	t       *testing.T
	py      string
	dir     string
	handler string
	consts  map[string]any
}

func newHandlerEnv(t *testing.T) *handlerEnv {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed; tw-tenant-handler needs it on the relay")
	}
	// Unix socket paths are limited to ~108 bytes; keep the tree short.
	dir, err := os.MkdirTemp("/tmp", "twh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	e := &handlerEnv{t: t, py: py, dir: dir}
	for _, d := range []string{"s", "m", "x", "bin"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.handler = filepath.Join(dir, "handler.py")
	e.write("handler.py", string(handlerScript), 0o644)
	e.write("harness.py", handlerHarness, 0o644)
	e.write("bin/xray", fakeXray, 0o755)
	e.consts = map[string]any{
		"SOCKET_DIR":    filepath.Join(dir, "s"),
		"MAP_DIR":       filepath.Join(dir, "m"),
		"MAP_OWNER_UID": os.Getuid(),
		"XRAY_CONFIG":   filepath.Join(dir, "x", "config.json"),
		"LOCK_PATH":     filepath.Join(dir, "x", ".tw-config.lock"),
		"XRAY_BIN":      filepath.Join(dir, "bin", "xray"),
		"IO_TIMEOUT":    5,
	}

	conf := map[string]any{
		"api": map[string]any{"tag": "api"},
		"inbounds": []any{
			map[string]any{"tag": "api-in", "port": 10085, "protocol": "dokodemo-door"},
			inbound("alpha", 30000, map[string]any{"id": alphaUUID, "email": "alpha"}),
			inbound("beta", 30001, map[string]any{"id": betaUUID, "email": "beta"},
				map[string]any{"id": betaUser1, "email": betaUser1}),
		},
	}
	data, _ := json.MarshalIndent(conf, "", "  ")
	e.write("x/config.json", string(data), 0o640)
	e.write("m/"+UserName("alpha"), "alpha\n", 0o600)
	e.write("m/"+UserName("beta"), "beta\n", 0o600)
	return e
}

func inbound(id string, port int, clients ...any) map[string]any {
	return map[string]any{
		"tag": "vless-in-" + id, "listen": "127.0.0.1", "port": port, "protocol": "vless",
		"settings":       map[string]any{"clients": clients, "decryption": "none"},
		"streamSettings": map[string]any{"network": "xhttp", "xhttpSettings": map[string]any{"path": "/tw/" + id}},
	}
}

func (e *handlerEnv) write(rel, content string, mode os.FileMode) {
	e.t.Helper()
	p := filepath.Join(e.dir, rel)
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		e.t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		e.t.Fatal(err)
	}
}

func (e *handlerEnv) read(rel string) string {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.dir, rel))
	if err != nil && !os.IsNotExist(err) {
		e.t.Fatal(err)
	}
	return string(b)
}

func (e *handlerEnv) inbounds() map[string]any {
	e.t.Helper()
	var conf struct{ Inbounds []map[string]any }
	if err := json.Unmarshal([]byte(e.read("x/config.json")), &conf); err != nil {
		e.t.Fatal(err)
	}
	out := map[string]any{}
	for _, ib := range conf.Inbounds {
		out[ib["tag"].(string)] = ib
	}
	return out
}

func (e *handlerEnv) clientIDs(tag string) []string {
	e.t.Helper()
	ib := e.inbounds()[tag].(map[string]any)
	var ids []string
	for _, c := range ib["settings"].(map[string]any)["clients"].([]any) {
		ids = append(ids, c.(map[string]any)["id"].(string))
	}
	return ids
}

// request connects to socket <dir>/s/<sockUser>.sock as a peer the handler
// resolves to peerUser, sends raw, and returns the reply and exit code.
func (e *handlerEnv) request(sockUser, peerUser, raw string) (string, int) {
	e.t.Helper()
	path := filepath.Join(e.consts["SOCKET_DIR"].(string), sockUser+".sock")
	return e.requestAt(path, peerUser, raw)
}

func (e *handlerEnv) requestAt(path, peerUser, raw string) (string, int) {
	e.t.Helper()
	return e.requestWith(path, peerUser, func(c net.Conn) {
		c.Write([]byte(raw))
		c.(*net.UnixConn).CloseWrite()
	})
}

// requestWith is requestAt with the client's sending side given by send.
func (e *handlerEnv) requestWith(path, peerUser string, send func(c net.Conn)) (string, int) {
	e.t.Helper()
	ln, err := net.Listen("unix", path)
	if err != nil {
		e.t.Fatal(err)
	}
	defer ln.Close()

	replyCh := make(chan string, 1)
	go func() {
		c, err := net.Dial("unix", path)
		if err != nil {
			replyCh <- "dial: " + err.Error()
			return
		}
		defer c.Close()
		send(c)
		c.SetReadDeadline(time.Now().Add(20 * time.Second))
		b, _ := io.ReadAll(c)
		replyCh <- string(b)
	}()
	conn, err := ln.Accept()
	if err != nil {
		e.t.Fatal(err)
	}
	f, err := conn.(*net.UnixConn).File()
	conn.Close()
	if err != nil {
		e.t.Fatal(err)
	}
	defer f.Close()

	opts, _ := json.Marshal(map[string]any{"consts": e.consts, "peers": map[string]string{fmt.Sprint(os.Getuid()): peerUser}})
	cmd := exec.Command(e.py, "-I", filepath.Join(e.dir, "harness.py"), e.handler, string(opts))
	cmd.Stdin = f
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	f.Close()
	code := 0
	if ee, ok := runErr.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if runErr != nil {
		e.t.Fatalf("running handler: %v\n%s", runErr, stderr.String())
	}
	if !strings.Contains(stderr.String(), "tw-tenant-handler: peer=") {
		e.t.Fatalf("handler did not log the request (crashed?):\n%s", stderr.String())
	}
	reply := <-replyCh
	if strings.Count(reply, "\n") != 1 || !strings.HasSuffix(reply, "\n") {
		e.t.Fatalf("reply must be exactly one line, got %q (stderr %s)", reply, stderr.String())
	}
	return strings.TrimSuffix(reply, "\n"), code
}

func TestHandlerOwnInboundOnly(t *testing.T) {
	e := newHandlerEnv(t)
	alpha, beta := UserName("alpha"), UserName("beta")
	betaBefore := e.inbounds()["vless-in-beta"]

	if reply, code := e.request(alpha, alpha, "list\n"); reply != "ok "+alphaUUID || code != 0 {
		t.Fatalf("list = %q (exit %d), want only alpha's own inbound", reply, code)
	}

	reply, code := e.request(alpha, alpha, "add "+newUser+"\n")
	if !strings.HasPrefix(reply, "ok added, applied via xray api") || code != 0 {
		t.Fatalf("add = %q (exit %d)", reply, code)
	}
	if got := e.clientIDs("vless-in-alpha"); !reflect.DeepEqual(got, []string{alphaUUID, newUser}) {
		t.Errorf("alpha clients after add = %v", got)
	}
	ib := e.inbounds()["vless-in-alpha"].(map[string]any)
	added := ib["settings"].(map[string]any)["clients"].([]any)[1].(map[string]any)
	if added["email"] != newUser || len(added) != 2 {
		t.Errorf("added client = %v, want {id, email: uuid}", added)
	}
	if !reflect.DeepEqual(e.inbounds()["vless-in-beta"], betaBefore) {
		t.Error("alpha's add changed beta's inbound")
	}
	st, err := os.Stat(filepath.Join(e.dir, "x", "config.json"))
	if err != nil || st.Mode().Perm() != 0o640 {
		t.Errorf("config.json mode after write = %v (%v), want 0640 preserved", st.Mode().Perm(), err)
	}
	calls := e.read("bin/calls.log")
	if !strings.Contains(calls, "xray api adu -s 127.0.0.1:10085 stdin:") ||
		!strings.Contains(calls, `"tag": "vless-in-alpha"`) ||
		!strings.Contains(calls, `"clients": [{"id": "`+newUser+`", "email": "`+newUser+`"}]`) {
		t.Errorf("api add must carry alpha's inbound with only the new client:\n%s", calls)
	}

	if reply, _ := e.request(alpha, alpha, "add "+newUser+"\n"); !strings.HasPrefix(reply, "ok present") {
		t.Errorf("re-add = %q, want ok present", reply)
	}

	// Removing beta's user through alpha's account finds nothing in alpha's
	// inbound and leaves beta untouched.
	if reply, _ := e.request(alpha, alpha, "remove "+betaUser1+"\n"); reply != "ok absent" {
		t.Errorf("cross-tenant remove = %q, want ok absent", reply)
	}
	if !reflect.DeepEqual(e.inbounds()["vless-in-beta"], betaBefore) {
		t.Error("alpha's remove changed beta's inbound")
	}
	if reply, code := e.request(alpha, alpha, "remove "+alphaUUID+"\n"); !strings.HasPrefix(reply, "err refusing to remove the server's own identity") || code == 0 {
		t.Errorf("removing the server's own id = %q (exit %d)", reply, code)
	}

	os.Remove(filepath.Join(e.dir, "bin", "calls.log"))
	if reply, _ := e.request(alpha, alpha, "remove "+newUser+"\n"); reply != "ok removed, applied via xray api" {
		t.Errorf("remove = %q", reply)
	}
	if got := e.clientIDs("vless-in-alpha"); !reflect.DeepEqual(got, []string{alphaUUID}) {
		t.Errorf("alpha clients after remove = %v", got)
	}
	if calls := e.read("bin/calls.log"); !strings.Contains(calls, "xray api rmu -s 127.0.0.1:10085 -tag=vless-in-alpha "+newUser) {
		t.Errorf("api remove must target alpha's inbound:\n%s", calls)
	}

	// Beta, through its own socket, sees and edits only beta.
	if reply, _ := e.request(beta, beta, "list\n"); reply != "ok "+betaUUID+" "+betaUser1 {
		t.Errorf("beta list = %q", reply)
	}
}

func TestHandlerRejectsBadRequests(t *testing.T) {
	e := newHandlerEnv(t)
	alpha := UserName("alpha")
	before := e.read("x/config.json")
	for name, raw := range map[string]string{
		"uppercase uuid":             "add " + strings.ToUpper(newUser) + "\n",
		"uuid with injected json":    `add x","level":0` + "\n",
		"path in place of uuid":      "add ../../etc/passwd\n",
		"extra field naming inbound": "add " + newUser + " vless-in-beta\n",
		"list another inbound":       "list vless-in-beta\n",
		"unknown verb":               "restart\n",
		"two requests":               "list\nlist\n",
		"no newline":                 "list",
		"oversized":                  "add " + strings.Repeat("a", 300) + "\n",
		"tab separator":              "add\t" + newUser + "\n",
		"non-ascii":                  "add " + newUser + "é\n",
		"empty":                      "",
	} {
		reply, code := e.request(alpha, alpha, raw)
		if !strings.HasPrefix(reply, "err ") || code == 0 {
			t.Errorf("%s: reply %q (exit %d), want err and non-zero exit", name, reply, code)
		}
	}
	if e.read("x/config.json") != before {
		t.Error("a rejected request modified the relay config")
	}
	if calls := e.read("bin/calls.log"); calls != "" {
		t.Errorf("a rejected request reached xray:\n%s", calls)
	}
}

// TestHandlerIdentity: the tenant comes only from the socket path, the
// peer uid and the root-owned mapping — never from the request.
func TestHandlerIdentity(t *testing.T) {
	e := newHandlerEnv(t)
	alpha, beta := UserName("alpha"), UserName("beta")
	before := e.read("x/config.json")

	for name, tc := range map[string]struct{ sock, peer string }{
		"peer is another tenant":   {alpha, beta},
		"peer is not a tenant":     {alpha, "root"},
		"socket name not a tenant": {"admin", "admin"},
	} {
		reply, code := e.request(tc.sock, tc.peer, "add "+newUser+"\n")
		if !strings.HasPrefix(reply, "err ") || code == 0 {
			t.Errorf("%s: reply %q (exit %d), want refusal", name, reply, code)
		}
	}

	// A socket outside the tenant socket directory is refused even when
	// its name and the peer agree.
	other := filepath.Join(e.dir, "x", alpha+".sock")
	if reply, _ := e.requestAt(other, alpha, "list\n"); !strings.HasPrefix(reply, "err connection did not arrive on a tenant socket") {
		t.Errorf("socket outside SOCKET_DIR: %q", reply)
	}

	// No mapping: an account that merely looks like a tenant is refused.
	gamma := UserName("gamma")
	if reply, _ := e.request(gamma, gamma, "list\n"); !strings.HasPrefix(reply, "err this account is not registered") {
		t.Errorf("unmapped account: %q", reply)
	}
	// A mapping naming another server id (e.g. alpha's file says "beta") is
	// refused: the account name must be derived from the mapped id.
	e.write("m/"+alpha, "beta\n", 0o600)
	if reply, _ := e.request(alpha, alpha, "list\n"); !strings.HasPrefix(reply, "err tenant registration does not match") {
		t.Errorf("mismatched mapping: %q", reply)
	}
	// A group/world-writable mapping is not trusted.
	e.write("m/"+alpha, "alpha\n", 0o666)
	if reply, _ := e.request(alpha, alpha, "list\n"); !strings.HasPrefix(reply, "err tenant registration on the relay is not trustworthy") {
		t.Errorf("writable mapping: %q", reply)
	}
	e.write("m/"+alpha, "alpha\n", 0o600)

	// A mapped tenant whose inbound is missing gets an error, not a new inbound.
	e.write("m/"+gamma, "gamma\n", 0o600)
	if reply, _ := e.request(gamma, gamma, "add "+newUser+"\n"); reply != "err inbound vless-in-gamma not found on the relay" {
		t.Errorf("missing inbound: %q", reply)
	}
	if e.read("x/config.json") != before {
		t.Error("a refused request modified the relay config")
	}
}

// TestHandlerNeverRestartsXray (M1): with the Xray API down a change is
// saved and reported as not live; nothing restarts Xray (the tenant could
// otherwise restart it at will, dropping every tenant's tunnels).
func TestHandlerNeverRestartsXray(t *testing.T) {
	e := newHandlerEnv(t)
	alpha := UserName("alpha")
	e.write("bin/xray-fail", "", 0o644)
	reply, code := e.request(alpha, alpha, "add "+newUser+"\n")
	if reply != "ok added, saved; "+LiveApplyFailed+" (xray api unreachable), takes effect on next xray restart" || code != 0 {
		t.Fatalf("add with api down = %q (exit %d)", reply, code)
	}
	if got := e.clientIDs("vless-in-alpha"); !reflect.DeepEqual(got, []string{alphaUUID, newUser}) {
		t.Errorf("the add must be persisted even when not applied live: %v", got)
	}
	reply, _ = e.request(alpha, alpha, "remove "+newUser+"\n")
	if reply != "ok removed, saved; "+LiveApplyFailed+" (xray api unreachable), takes effect on next xray restart" {
		t.Errorf("remove with api down = %q", reply)
	}
	for i := 0; i < 5; i++ {
		e.request(alpha, alpha, "add "+newUser+"\n")
	}
	calls := e.read("bin/calls.log")
	if strings.Contains(calls, "systemctl") || strings.Contains(calls, "restart") {
		t.Errorf("the handler restarted xray:\n%s", calls)
	}

	// Once the API is back, sending the remove again applies its live part
	// although the file no longer lists the user.
	os.Remove(filepath.Join(e.dir, "bin", "xray-fail"))
	e.request(alpha, alpha, "remove "+newUser+"\n")
	os.Remove(filepath.Join(e.dir, "bin", "calls.log"))
	if reply, _ := e.request(alpha, alpha, "remove "+newUser+"\n"); reply != "ok absent" {
		t.Errorf("retried remove = %q, want ok absent", reply)
	}
	if calls := e.read("bin/calls.log"); !strings.Contains(calls, "xray api rmu -s 127.0.0.1:10085 -tag=vless-in-alpha "+newUser) {
		t.Errorf("a retried remove must reach the running xray:\n%s", calls)
	}
}

// TestHandlerAPIOutcomes (M1): `xray api adu|rmu` exit 0 whatever happened,
// so the outcome is read from their exact output lines — never a substring.
func TestHandlerAPIOutcomes(t *testing.T) {
	alpha := UserName("alpha")
	rpc := "rpc error: code = Unknown desc = "
	notLoaded := rpc + "app/proxyman/command: failed to get handler: vless-in-alpha > app/proxyman/inbound: handler not found: vless-in-alpha"
	rejected := "ok %s, saved; " + LiveApplyFailed + " (xray api rejected the change), takes effect on next xray restart"
	for name, tc := range map[string]struct {
		verb, file, out, want string
		code                  int
	}{
		"add applied": {"add", "adu-out",
			"processing inbound: vless-in-alpha\nadd user: " + newUser + "\nresult: ok\nAdded 1 user(s) in total.\n",
			"ok added, applied via xray api", 0},
		"add already live": {"add", "adu-out",
			"processing inbound: vless-in-alpha\nadd user: " + newUser + "\n" + rpc + "proxy/vless: User " + newUser + " already exists.\nAdded 0 user(s) in total.\n",
			"ok added, applied via xray api", 0},
		"add inbound not loaded": {"add", "adu-out",
			"processing inbound: vless-in-alpha\nadd user: " + newUser + "\n" + notLoaded + "\nAdded 0 user(s) in total.\n",
			"err inbound vless-in-alpha is not loaded in the relay's xray", 1},
		"add other user exists": {"add", "adu-out",
			"add user: " + newUser + "\n" + rpc + "proxy/vless: User " + betaUser1 + " already exists.\nAdded 0 user(s) in total.\n",
			strings.Replace(rejected, "%s", "added", 1), 0},
		"add summary not exact": {"add", "adu-out",
			"add user: " + newUser + "\nAdded 10 user(s) in total.\n",
			strings.Replace(rejected, "%s", "added", 1), 0},
		"add exists only as substring": {"add", "adu-out",
			"add user: " + newUser + "\n" + rpc + "failed: User " + newUser + " already exists. or so\nAdded 0 user(s) in total.\n",
			strings.Replace(rejected, "%s", "added", 1), 0},
		"remove applied": {"remove", "rmu-out",
			"remove user: " + betaUser1 + "\nRemoved 1 user(s) in total.\n",
			"ok removed, applied via xray api", 0},
		"remove not live": {"remove", "rmu-out",
			"remove user: " + betaUser1 + "\n" + rpc + "proxy/vless: User " + betaUser1 + " not found.\nRemoved 0 user(s) in total.\n",
			"ok removed, applied via xray api", 0},
		"remove inbound not loaded": {"remove", "rmu-out",
			"remove user: " + betaUser1 + "\n" + strings.ReplaceAll(notLoaded, "alpha", "beta") + "\nRemoved 0 user(s) in total.\n",
			"err inbound vless-in-beta is not loaded in the relay's xray", 1},
		"remove other not found": {"remove", "rmu-out",
			"remove user: " + betaUser1 + "\n" + rpc + "proxy/vless: User " + newUser + " not found.\nRemoved 0 user(s) in total.\n",
			strings.Replace(rejected, "%s", "removed", 1), 0},
	} {
		e := newHandlerEnv(t)
		e.write("bin/"+tc.file, tc.out, 0o644)
		user, uuid := alpha, newUser
		if tc.verb == "remove" {
			user, uuid = UserName("beta"), betaUser1
		}
		if reply, code := e.request(user, user, tc.verb+" "+uuid+"\n"); reply != tc.want || code != tc.code {
			t.Errorf("%s: reply %q (exit %d), want %q (exit %d)", name, reply, code, tc.want, tc.code)
		}
	}
}

// TestHandlerClientCap (L4): a tenant inbound holds at most MAX_CLIENTS.
func TestHandlerClientCap(t *testing.T) {
	e := newHandlerEnv(t)
	e.consts["MAX_CLIENTS"] = 3
	alpha := UserName("alpha")
	extra := "dddddddd-3333-4000-8000-000000000005"
	for _, u := range []string{newUser, extra} {
		if reply, _ := e.request(alpha, alpha, "add "+u+"\n"); !strings.HasPrefix(reply, "ok added") {
			t.Fatalf("add %s below the cap = %q", u, reply)
		}
	}
	before := e.read("x/config.json")
	reply, code := e.request(alpha, alpha, "add eeeeeeee-4444-4000-8000-000000000006\n")
	if reply != "err inbound vless-in-alpha already holds 3 clients, the most a tenant may have" || code == 0 {
		t.Errorf("add beyond the cap = %q (exit %d)", reply, code)
	}
	if e.read("x/config.json") != before {
		t.Error("a refused add modified the relay config")
	}
	if reply, _ := e.request(alpha, alpha, "add "+extra+"\n"); !strings.HasPrefix(reply, "ok present") {
		t.Errorf("re-adding an existing client at the cap = %q", reply)
	}
}

// setInbound replaces (or appends) the inbound tagged vless-in-<id> in the
// relay config.
func (e *handlerEnv) setInbound(id string, port int, clients ...any) {
	e.t.Helper()
	var conf map[string]any
	if err := json.Unmarshal([]byte(e.read("x/config.json")), &conf); err != nil {
		e.t.Fatal(err)
	}
	ibs := conf["inbounds"].([]any)
	ib := inbound(id, port, clients...)
	replaced := false
	for i, x := range ibs {
		if x.(map[string]any)["tag"] == ib["tag"] {
			ibs[i], replaced = ib, true
		}
	}
	if !replaced {
		ibs = append(ibs, ib)
	}
	conf["inbounds"] = ibs
	data, _ := json.MarshalIndent(conf, "", "  ")
	e.write("x/config.json", string(data), 0o640)
}

// TestHandlerRefusesUUIDShapedServerID: a tenant whose server id S is
// UUID-shaped could `add S`, giving its inbound a second client with email S
// (its own identity's email); Xray then refuses to load the shared config on
// its next start, taking every tenant offline.
func TestHandlerRefusesUUIDShapedServerID(t *testing.T) {
	e := newHandlerEnv(t)
	const s = "dddddddd-4444-4444-8444-444444444444"
	e.setInbound(s, 30002, map[string]any{"id": "dddddddd-0000-4000-8000-000000000000", "email": s})
	e.write("m/"+UserName(s), s+"\n", 0o600)
	before := e.read("x/config.json")

	reply, code := e.request(UserName(s), UserName(s), "add "+s+"\n")
	if !strings.HasPrefix(reply, "err ") || code == 0 {
		t.Errorf("add <server id> = %q (exit %d), want refusal", reply, code)
	}
	if e.read("x/config.json") != before {
		t.Error("a refused add modified the relay config")
	}

	alpha := UserName("alpha")
	if reply, _ := e.request(alpha, alpha, "add "+newUser+"\n"); !strings.HasPrefix(reply, "ok added") {
		t.Errorf("ordinary add = %q", reply)
	}
}

// TestHandlerRefusesDuplicateEmail: an add whose uuid is already the email of
// a client in the inbound (compared case-insensitively, as Xray does), or one
// that would leave duplicate emails behind, is refused and saves nothing.
func TestHandlerRefusesDuplicateEmail(t *testing.T) {
	e := newHandlerEnv(t)
	alpha := UserName("alpha")
	own := map[string]any{"id": alphaUUID, "email": "alpha"}

	e.setInbound("alpha", 30000, own, map[string]any{"id": betaUser1, "email": strings.ToUpper(newUser)})
	before := e.read("x/config.json")
	reply, code := e.request(alpha, alpha, "add "+newUser+"\n")
	if reply != "err "+newUser+" is already the email of a client in inbound vless-in-alpha" || code == 0 {
		t.Errorf("add colliding with an email = %q (exit %d)", reply, code)
	}
	if e.read("x/config.json") != before {
		t.Error("a refused add modified the relay config")
	}

	e.setInbound("alpha", 30000, own,
		map[string]any{"id": betaUser1, "email": "dup"},
		map[string]any{"id": betaUUID, "email": "DUP"})
	before = e.read("x/config.json")
	reply, code = e.request(alpha, alpha, "add "+newUser+"\n")
	if reply != "err inbound vless-in-alpha would hold duplicate client emails" || code == 0 {
		t.Errorf("add to an inbound with duplicate emails = %q (exit %d)", reply, code)
	}
	if e.read("x/config.json") != before {
		t.Error("a refused add modified the relay config")
	}

	e.setInbound("alpha", 30000, own)
	if reply, _ := e.request(alpha, alpha, "add "+newUser+"\n"); !strings.HasPrefix(reply, "ok added") {
		t.Errorf("ordinary add = %q", reply)
	}
	if got := e.clientIDs("vless-in-alpha"); !reflect.DeepEqual(got, []string{alphaUUID, newUser}) {
		t.Errorf("alpha clients after add = %v", got)
	}
}

// TestHandlerRechecksRegistrationUnderLock (L2): a request that waited for
// the config lock while the admin un-enrolled its tenant does nothing.
func TestHandlerRechecksRegistrationUnderLock(t *testing.T) {
	e := newHandlerEnv(t)
	alpha := UserName("alpha")
	before := e.read("x/config.json")
	lock, err := os.OpenFile(e.consts["LOCK_PATH"].(string), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	type result struct {
		reply string
		code  int
	}
	done := make(chan result, 1)
	go func() {
		reply, code := e.request(alpha, alpha, "add "+newUser+"\n")
		done <- result{reply, code}
	}()
	time.Sleep(1500 * time.Millisecond) // the handler identified alpha and waits for the lock
	if err := os.Remove(filepath.Join(e.dir, "m", alpha)); err != nil {
		t.Fatal(err)
	}
	syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	r := <-done
	if r.reply != "err this account is not registered as a tenant on the relay" || r.code == 0 {
		t.Errorf("request after un-enroll = %q (exit %d)", r.reply, r.code)
	}
	if e.read("x/config.json") != before {
		t.Error("a request of an un-enrolled tenant modified the relay config")
	}
	if calls := e.read("bin/calls.log"); calls != "" {
		t.Errorf("a request of an un-enrolled tenant reached xray:\n%s", calls)
	}
}

// TestHandlerRequestDeadline (L1): the request line has IO_TIMEOUT in total,
// not per read, so trickling bytes cannot hold the handler.
func TestHandlerRequestDeadline(t *testing.T) {
	e := newHandlerEnv(t)
	e.consts["IO_TIMEOUT"] = 1
	alpha := UserName("alpha")
	start := time.Now()
	reply, code := e.requestWith(filepath.Join(e.consts["SOCKET_DIR"].(string), alpha+".sock"), alpha, func(c net.Conn) {
		for i := 0; i < 8; i++ {
			if _, err := c.Write([]byte("l")); err != nil {
				return
			}
			time.Sleep(400 * time.Millisecond)
		}
	})
	if reply != "err timed out waiting for the request" || code == 0 {
		t.Errorf("trickled request = %q (exit %d)", reply, code)
	}
	if d := time.Since(start); d > 2500*time.Millisecond {
		t.Errorf("handler waited %v for a trickled request, want about IO_TIMEOUT", d)
	}
}

// requestHarness runs tw-tenant-request with its socket dir and account
// lookup pointed at the test.
const requestHarness = `
import importlib.machinery, importlib.util, sys
loader = importlib.machinery.SourceFileLoader("tw_tenant_request", sys.argv[1])
spec = importlib.util.spec_from_loader(loader.name, loader)
r = importlib.util.module_from_spec(spec)
loader.exec_module(r)
r.SOCKET_DIR = sys.argv[2]
r.account = lambda: sys.argv[3]
if len(sys.argv) > 4:
    r.READ_TIMEOUT = int(sys.argv[4])
sys.exit(r.main())
`

func TestRequestClient(t *testing.T) {
	e := newHandlerEnv(t)
	e.write("request.py", string(requestScript), 0o644)
	e.write("reqharness.py", requestHarness, 0o644)
	user := UserName("alpha")
	sockDir := e.consts["SOCKET_DIR"].(string)
	ln, err := net.Listen("unix", filepath.Join(sockDir, user+".sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- "accept: " + err.Error()
			return
		}
		b, _ := io.ReadAll(c)
		c.Write([]byte("ok fine\n"))
		c.Close()
		got <- string(b)
	}()

	run := func(stdin string) (string, int) {
		cmd := exec.Command(e.py, "-I", filepath.Join(e.dir, "reqharness.py"), filepath.Join(e.dir, "request.py"), sockDir, user)
		cmd.Stdin = strings.NewReader(stdin)
		// The tenant controls SSH_ORIGINAL_COMMAND; it must not matter.
		cmd.Env = append(os.Environ(), "SSH_ORIGINAL_COMMAND=add "+newUser)
		out, err := cmd.Output()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}

	if out, code := run("list\nignored trailing input"); out != "ok fine\n" || code != 0 {
		t.Errorf("client output %q (exit %d)", out, code)
	}
	if sent := <-got; sent != "list\n" {
		t.Errorf("client forwarded %q, want exactly the first stdin line", sent)
	}
	if out, code := run(strings.Repeat("a", 200) + "\n"); !strings.HasPrefix(out, "err ") || code == 0 {
		t.Errorf("oversized request: %q (exit %d)", out, code)
	}
	if out, code := run(""); !strings.HasPrefix(out, "err ") || code == 0 {
		t.Errorf("empty request: %q (exit %d)", out, code)
	}
	ln.Close()
	if out, code := run("list\n"); !strings.HasPrefix(out, "err tenant request channel unavailable") || code == 0 {
		t.Errorf("no handler socket: %q (exit %d)", out, code)
	}

	// L1: a session that never sends its line ends after READ_TIMEOUT.
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pw.Close()
	pw.Write([]byte("lis"))
	cmd := exec.Command(e.py, "-I", filepath.Join(e.dir, "reqharness.py"), filepath.Join(e.dir, "request.py"), sockDir, user, "1")
	cmd.Stdin = pr
	start := time.Now()
	out, err := cmd.Output()
	pr.Close()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 || string(out) != "err timed out waiting for the request line on stdin\n" {
		t.Errorf("silent stdin: %q (%v)", out, err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("client waited %v for its request line", d)
	}
}
