//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestBench is the opt-in throughput & footprint benchmark (`make bench`).
// It is deliberately NOT a TestE2E subtest: ~30 min of transfers and two
// host kernel modules (wireguard, sch_netem) do not belong in the always-on
// suite. Design: .claude/superpowers/specs/2026-08-29-tunnel-benchmark-design.md.
//
// Topology reuse: the relay is provisioned and the server enrolled by the
// SAME scenarios the e2e suite runs (real install script, real invite/join),
// then a throwaway `bench` Unix user + sshd, iperf3 and netserver are started
// on the server container as the targets every transport must reach.

const (
	benchUser        = "bench"
	benchFile        = "/bench/bench.bin"
	benchWarmFile    = "/bench/warm.bin"
	benchKey         = "/bench/id_ed25519"
	benchOut         = "/bench/out"
	wgServerIP       = "10.99.0.1"
	wgClientIP       = "10.99.0.2"
	wgPort           = "51820"
	sshdPort         = "22"
	iperfPort        = "5201"
	netserverPort    = "12865"
	netperfDataPort  = "12866" // netserver's data socket; tw maps it 1:1 (see spec, decision 7)
	netperfLocalPort = "12867" // netperf's client-side data source port
	twSSHPort        = "18022"
	twIperfPort      = "18201"
	twNetserverPort  = "18865"
	iperfSeconds     = 10
	netperfSeconds   = 10

	scpOptions = "-c aes128-gcm@openssh.com -o Compression=no -o StrictHostKeyChecking=no " +
		"-o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -i " + benchKey
)

type benchCfg struct {
	sizeGB, runs int
	wan          string  // raw BENCH_WAN value, "off" for LAN only
	delayMs      float64 // per side (half the RTT)
	lossPct      float64 // per side
}

func benchConfig(t *testing.T) benchCfg {
	t.Helper()
	cfg := benchCfg{sizeGB: 5, runs: 3, wan: "30ms:0.1%"}
	if v := os.Getenv("BENCH_SIZE_GB"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("BENCH_SIZE_GB=%q: want a positive integer", v)
		}
		cfg.sizeGB = n
	}
	if v := os.Getenv("BENCH_RUNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("BENCH_RUNS=%q: want a positive integer", v)
		}
		cfg.runs = n
	}
	if v := os.Getenv("BENCH_WAN"); v != "" {
		cfg.wan = v
	}
	if cfg.wan != "off" {
		m := regexp.MustCompile(`^(\d+(?:\.\d+)?)ms:(\d+(?:\.\d+)?)%$`).FindStringSubmatch(cfg.wan)
		if m == nil {
			t.Fatalf("BENCH_WAN=%q: want <rtt>ms:<loss>%% (e.g. 30ms:0.1%%) or off", cfg.wan)
		}
		rtt, _ := strconv.ParseFloat(m[1], 64)
		loss, _ := strconv.ParseFloat(m[2], 64)
		cfg.delayMs, cfg.lossPct = rtt/2, loss
	}
	return cfg
}

type benchCondition struct {
	name, label string
	netem       string // tc netem arguments; "" = none
}

func benchConditions(cfg benchCfg) []benchCondition {
	conds := []benchCondition{{name: condLAN, label: "LAN (0 ms RTT, no loss)"}}
	if cfg.wan != "off" {
		args := fmt.Sprintf("delay %gms", cfg.delayMs)
		if cfg.lossPct > 0 {
			args += fmt.Sprintf(" loss %g%%", cfg.lossPct)
		}
		// netem's default 1000-packet queue would itself drop at Gbit rates
		// with a 15 ms delay; raise it so only the configured loss applies.
		args += " limit 100000"
		conds = append(conds, benchCondition{
			name:  condWAN,
			label: fmt.Sprintf("WAN (%g ms RTT, %g %% loss)", cfg.delayMs*2, cfg.lossPct),
			netem: args,
		})
	}
	return conds
}

// benchTarget is where the client finds the server's three services for a
// given transport.
type benchTarget struct {
	host                  string
	ssh, iperf, netserver string
}

type benchTransport struct {
	name     string
	setup    func(t *testing.T) benchTarget
	teardown func(t *testing.T)
	rssProcs map[string]string // "<service> / <label>" → cmdline substring
}

func TestBench(t *testing.T) {
	cfg := benchConfig(t)
	benchPreflight(t, cfg)
	waitForRelayBoot(t)

	// Same product-driven provisioning as the e2e suite. Stop on failure:
	// nothing below makes sense without a working relay + enrolled server.
	if !t.Run("RelayInstall", testRelayInstall) {
		t.Fatal("relay provisioning failed")
	}
	if !t.Run("ServerJoin", testServerJoin) {
		t.Fatal("server enrollment failed")
	}

	res := &benchResults{Env: captureBenchEnv(t, cfg)}
	defer writeBenchResults(t, res)

	payloads := benchFixture(t, cfg)
	res.Env.ScpForm = detectScpForm(t, benchTarget{host: "server", ssh: sshdPort})
	t.Logf("scp form: %s", res.Env.ScpForm)

	// Conditions are recorded after benchFixture so each one can carry the
	// scp payload size it actually uses (LAN: the full file; WAN: the
	// smaller warm file — see runInstruments).
	for _, c := range benchConditions(cfg) {
		info := benchConditionInfo{Name: c.name, Label: c.label, ScpPayloadBytes: payloads.full}
		if c.name == condWAN {
			info.ScpPayloadBytes = payloads.warm
		}
		res.Conditions = append(res.Conditions, info)
	}

	t.Cleanup(func() { clearNetem(t) })
	for _, tr := range benchTransports() {
		t.Logf("=== transport %s: setup", tr.name)
		target := tr.setup(t)
		for _, c := range benchConditions(cfg) {
			t.Logf("=== transport %s / condition %s", tr.name, c.name)
			applyNetem(t, c)
			runInstruments(t, cfg, res, tr.name, c.name, target, payloads)
			if c.name == condWAN {
				collectBenchDiagnostics(t, res, tr.name, c.name)
			}
			clearNetem(t)
		}
		rss := benchRSS{Transport: tr.name, PeakKB: map[string]uint64{}}
		if tr.name == trSSH {
			// sshd/iperf3/netserver are the processes common to every
			// transport, sampled here (after ssh's own connections closed)
			// as the shared idle-listener baseline — not a footprint
			// specific to "Pure SSH".
			rss.Label = "Baseline (all transports; idle listeners after the runs)"
		}
		if len(tr.rssProcs) == 0 {
			// Kernel WireGuard has no userspace process to sample; report an
			// explicit 0 row rather than leaving this transport out of the
			// memory table (matches the renderer's "reported as 0" note).
			rss.PeakKB["kernel (no userspace process)"] = 0
		}
		for label, substr := range tr.rssProcs {
			svc := strings.SplitN(label, " / ", 2)[0]
			rss.PeakKB[label] = peakRSSKB(t, svc, substr)
		}
		res.RSS = append(res.RSS, rss)
		t.Logf("=== transport %s: teardown", tr.name)
		tr.teardown(t)
	}
}

// benchPreflight fails fast with the exact fix when the host or images are
// not ready, before any 5 GB file is generated.
func benchPreflight(t *testing.T, cfg benchCfg) {
	t.Helper()
	out, err := compose("ps", "--status", "running", "--services").CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose ps failed — did you run `make bench`? %v\n%s", err, out)
	}
	for _, svc := range []string{"relay", "admin", "server", "client"} {
		if !containsLine(string(out), svc) {
			t.Fatalf("service %q is not running — start the topology with `make bench`\n%s", svc, out)
		}
	}
	if _, err := os.Stat("/sys/module/wireguard"); err != nil {
		t.Fatal("WireGuard kernel module not loaded on the Docker host — run: sudo modprobe wireguard sch_netem")
	}
	if cfg.wan != "off" {
		if _, err := os.Stat("/sys/module/sch_netem"); err != nil {
			t.Fatal("netem kernel module not loaded on the Docker host — run: sudo modprobe wireguard sch_netem (or BENCH_WAN=off)")
		}
	}
	execIn(t, "client", "which wg ip tc iperf3 netperf scp ssh-keygen ethtool >/dev/null")
	execIn(t, "server", "which wg ip tc iperf3 netserver sshd useradd ethtool >/dev/null")
	execIn(t, "relay", "which ethtool ip >/dev/null")
	// A previous run killed with E2E_KEEP=1 may have left netem applied.
	clearNetem(t)

	// netem drops/delays per packet at the egress qdisc. On the plain
	// bridge path (ssh, and tw's underlying xray/SSH-forward connection)
	// TCP segmentation/generic offloads let the kernel hand the NIC driver
	// up to 64 KiB "super-packets" that only fragment into real ≤1500-byte
	// packets at the wire — so a 0.1% per-packet loss rate hits that path
	// ~40x less often per byte than WireGuard, whose UDP datagrams are
	// always real ≤1500-byte packets. Disabling this here, in preflight,
	// BEFORE RelayInstall/ServerJoin run, ensures every benchmark
	// connection — including tw's long-lived server→relay tunnel opened
	// during ServerJoin — is established without offload capabilities
	// baked into its socket/GSO state. The relay is included too: left
	// alone, its egress carried GSO super-packets into the client, which
	// under-counted tw's wire bytes and left the relay→client leg with
	// offloads on while every other leg had them off — uniform state on
	// all three containers is the only fair setup. Never re-enabled at the
	// end: containers are disposable and `make bench` always starts from a
	// fresh topology.
	for _, svc := range []string{"client", "server", "relay"} {
		execIn(t, svc, "ethtool -K eth0 tso off gso off gro off")
		out := execIn(t, svc, "ethtool -k eth0")
		for _, feature := range []string{"tcp-segmentation-offload", "generic-segmentation-offload", "generic-receive-offload"} {
			if !ethtoolFeatureOff(out, feature) {
				fatalf(t, "%s eth0 %s did not report off after ethtool -K:\n%s", svc, feature, out)
			}
		}
	}

	// ethtool -K alone is not enough for TCP: Linux always builds GSO skbs
	// for TCP sockets regardless of device features — sk_setup_caps() ORs
	// NETIF_F_GSO into sk_route_caps unconditionally for TCP — so netem
	// still saw ~6-segment skbs and dropped/delayed them as single units
	// ("Sent ... pkt" in `tc -s qdisc show` counts skbs, not wire packets),
	// which is why the ssh and tw legs measured ~0.015% loss against
	// WireGuard's true configured ~0.104% (see run 4's diagnostics). The
	// per-socket cap sk_gso_max_segs is seeded from the device's
	// gso_max_segs at socket setup, so setting it to 1 here — before
	// RelayInstall/ServerJoin open any sockets — makes every benchmark TCP
	// connection, including tw's server→relay tunnel, emit one-segment
	// skbs; netem's per-skb loss then becomes per 1500-byte packet for
	// every transport, matching WireGuard's per-UDP-datagram loss.
	gsoMaxSegsRe := regexp.MustCompile(`gso_max_segs 1( |$)`)
	for _, svc := range []string{"client", "server", "relay"} {
		execIn(t, svc, "ip link set dev eth0 gso_max_segs 1")
		out := execIn(t, svc, "ip -d link show eth0")
		if !gsoMaxSegsRe.MatchString(out) {
			fatalf(t, "%s eth0 gso_max_segs did not report 1 after ip link set:\n%s", svc, out)
		}
	}
}

// ethtoolFeatureOff reports whether `ethtool -k` output shows feature as
// off. Accepts both "<feature>: off" and "<feature>: off [fixed]" (a fixed
// feature the driver hardwired to off) as off; anything else — including
// "on [fixed]", where the driver refuses to disable it — is not.
func ethtoolFeatureOff(out, feature string) bool {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == feature+": off" || line == feature+": off [fixed]" {
			return true
		}
	}
	return false
}

func captureBenchEnv(t *testing.T, cfg benchCfg) benchEnv {
	t.Helper()
	e := benchEnv{
		Generated:  time.Now().UTC().Format(time.RFC3339),
		Cores:      hostCores(t),
		CPUModel:   hostCPUModel(t),
		Kernel:     strings.TrimSpace(readHostFile(t, "/proc/sys/kernel/osrelease")),
		Docker:     strings.TrimSpace(hostCmd(t, "docker", "version", "--format", "{{.Server.Version}}")),
		TW:         strings.TrimSpace(execIn(t, "client", "tw --version")),
		OpenSSH:    strings.TrimSpace(execIn(t, "client", "ssh -V 2>&1")),
		Iperf3:     strings.TrimSpace(execIn(t, "client", "iperf3 -v 2>&1 | head -1")),
		Netperf:    strings.TrimSpace(execIn(t, "client", "netperf -V 2>&1 | head -1")),
		WireGuard:  wireGuardVersion(t),
		Image:      "debian:bookworm-slim (e2e/images/tw)",
		WAN:        cfg.wan,
		Offloads:   "tso/gso/gro off + gso_max_segs 1 on client, server and relay eth0 for the whole run (TCP always builds GSO skbs unless gso_max_segs caps it; netem drops per skb, so this makes loss per 1500-byte packet for every transport; LAN ceilings are therefore software-segmentation ceilings)",
		ScpOptions: scpOptions,
		Runs:       cfg.runs,
		SizeGB:     cfg.sizeGB,
	}
	return e
}

func readHostFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// wireGuardVersion reports the out-of-tree module version when present, or
// notes a built-in kernel WireGuard (no version file to read) rather than
// failing the whole run over a missing sysfs file.
func wireGuardVersion(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("/sys/module/wireguard/version")
	if err != nil {
		return "kernel (built-in, version unknown)"
	}
	return "kernel module " + strings.TrimSpace(string(b))
}

func hostCmd(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func hostCores(t *testing.T) int {
	t.Helper()
	n := 0
	for _, l := range strings.Split(readHostFile(t, "/proc/cpuinfo"), "\n") {
		if strings.HasPrefix(l, "processor") {
			n++
		}
	}
	return n
}

func hostCPUModel(t *testing.T) string {
	t.Helper()
	for _, l := range strings.Split(readHostFile(t, "/proc/cpuinfo"), "\n") {
		if strings.HasPrefix(l, "model name") {
			if i := strings.Index(l, ":"); i >= 0 {
				return strings.TrimSpace(l[i+1:])
			}
		}
	}
	return "unknown"
}

// benchPayloads holds the two scp payload sizes benchFixture prepares: the
// full LAN file and the smaller warm file WAN also reuses as its scp
// payload (see runInstruments).
type benchPayloads struct {
	full, warm uint64
}

// benchFixture prepares the server-side targets and the client key, and
// returns the LAN (full) and WAN (warm-file) scp payload sizes in bytes.
// Re-runnable: kills leftovers first.
func benchFixture(t *testing.T, cfg benchCfg) benchPayloads {
	t.Helper()
	t.Log("fixture: killing leftover sshd/iperf3/netserver from a previous run")
	killMatching(t, "server", "/usr/sbin/sshd")
	killMatching(t, "server", "iperf3 -s")
	killMatching(t, "server", "netserver")

	needKB := uint64(cfg.sizeGB)*1024*1024 + 300*1024
	execIn(t, "server", fmt.Sprintf(`rm -f %s %s; avail=$(df -k --output=avail /bench | tail -1); `+
		`[ "$avail" -gt %d ] || { echo "/bench tmpfs too small: ${avail} kB free, need %d kB — raise the server tmpfs size in e2e/docker-compose.yaml"; exit 1; }`,
		benchFile, benchWarmFile, needKB, needKB))

	t.Logf("fixture: generating %d GiB of /dev/urandom on the server tmpfs (takes a while)", cfg.sizeGB)
	execIn(t, "server", fmt.Sprintf("head -c %dG /dev/urandom > %s && head -c 256M /dev/urandom > %s && chmod 644 /bench/*.bin",
		cfg.sizeGB, benchFile, benchWarmFile))
	payload, err := strconv.ParseUint(strings.TrimSpace(execIn(t, "server", "stat -c %s "+benchFile)), 10, 64)
	if err != nil {
		fatalf(t, "stat payload: %v", err)
	}
	warmPayload, err := strconv.ParseUint(strings.TrimSpace(execIn(t, "server", "stat -c %s "+benchWarmFile)), 10, 64)
	if err != nil {
		fatalf(t, "stat warm payload: %v", err)
	}

	execIn(t, "client", "rm -f "+benchKey+" "+benchKey+".pub && ssh-keygen -q -t ed25519 -N '' -f "+benchKey+
		" && cp "+benchKey+".pub /shared/bench-id.pub")
	execIn(t, "server", `set -e; mkdir -p /run/sshd; ssh-keygen -A >/dev/null 2>&1; `+
		`id `+benchUser+` >/dev/null 2>&1 || useradd -m -s /bin/sh `+benchUser+`; `+
		// useradd leaves the shadow password "!" (locked); with UsePAM=no,
		// sshd's own account_locked() check rejects even pubkey auth against a
		// locked account, independent of PasswordAuthentication. passwd -d
		// clears the password (empty, not "!"), unlocking that check while
		// PasswordAuthentication=no still refuses any password-based login.
		`passwd -d `+benchUser+`; `+
		`install -d -m 700 -o `+benchUser+` -g `+benchUser+` /home/`+benchUser+`/.ssh; `+
		`install -m 600 -o `+benchUser+` -g `+benchUser+` /shared/bench-id.pub /home/`+benchUser+`/.ssh/authorized_keys; `+
		// UsePAM=no: pubkey-only auth has no need for PAM, and the container
		// lacks CAP_AUDIT_CONTROL for pam_loginuid.so under an audit-enabled
		// kernel, which would otherwise reject the session. VERBOSE + -E logs
		// auth failures to /var/log/sshd.log for diagnosis.
		`/usr/sbin/sshd -o PasswordAuthentication=no -o PermitRootLogin=no -o UsePAM=no -o LogLevel=VERBOSE -E /var/log/sshd.log`)
	execDetached(t, "server", "iperf3 -s -p "+iperfPort+" > /var/log/iperf3.log 2>&1")
	execIn(t, "server", "netserver -p "+netserverPort+" >/dev/null") // daemonizes itself
	for _, p := range []string{sshdPort, iperfPort, netserverPort} {
		waitFor(t, "server port "+p, 30*time.Second, func() (bool, string) {
			_, err := execInOK("server", "nc -z 127.0.0.1 "+p)
			return err == nil, "not listening yet"
		})
	}
	return benchPayloads{full: payload, warm: warmPayload}
}

// detectScpForm picks the first working way to copy to /dev/null. OpenSSH
// 9.x scp uses SFTP by default, which may refuse a device node as the
// destination; `-O` is the legacy protocol; `ssh cat` is the last resort —
// all three move the same bytes over the same SSH connection.
// detectScpForm exercises each candidate through timedScript's exact
// framing (the same wrapper timedExec runs commands with during the real
// measurements), so a form is only selected if it works the way it will
// actually be run — see the "ssh cat" redirect note on scpCmd.
func detectScpForm(t *testing.T, target benchTarget) string {
	t.Helper()
	for _, form := range []string{"scp", "scp -O", "ssh cat"} {
		raw, err := execInOK("client", timedScript(scpCmd(form, target, benchWarmFile)))
		if err != nil {
			t.Logf("scp form %q not usable: %v\n%s", form, err, raw)
			continue
		}
		_, rc, out, ok := parseBenchT(raw)
		if !ok {
			t.Logf("scp form %q: no BENCH_T line in output:\n%s", form, raw)
			continue
		}
		if rc != "0" {
			t.Logf("scp form %q exited %s:\n%s", form, rc, out)
			continue
		}
		return form
	}
	fatalf(t, "no scp form works against the plain-SSH target; is sshd up on the server? check /var/log/sshd.log on the server")
	return ""
}

// scpCmd never redirects the tool's own stdout itself except for "ssh cat",
// which must: without it, timedScript's own `> /bench/out 2>&1` wrapper
// would be the ONLY redirect on the ssh process's stdout, and 5 GiB of file
// content would land in benchOut instead of being discarded. Wrapping it in
// `sh -c '...'` makes the inner shell consume ssh's stdout via its OWN
// `> /dev/null` redirect; the outer command (the `sh -c` invocation itself)
// then has empty stdout, so timedScript's wrapper only ever sees that.
func scpCmd(form string, target benchTarget, file string) string {
	switch form {
	case "scp":
		return fmt.Sprintf("scp %s -P %s %s@%s:%s /dev/null", scpOptions, target.ssh, benchUser, target.host, file)
	case "scp -O":
		return fmt.Sprintf("scp -O %s -P %s %s@%s:%s /dev/null", scpOptions, target.ssh, benchUser, target.host, file)
	case "ssh cat":
		return fmt.Sprintf("sh -c 'ssh %s -p %s %s@%s cat %s > /dev/null'", scpOptions, target.ssh, benchUser, target.host, file)
	}
	panic("unknown scp form " + form)
}

func iperfCmd(target benchTarget, streams, seconds int) string {
	return fmt.Sprintf("iperf3 -c %s -p %s -R -t %d -P %d -J", target.host, target.iperf, seconds, streams)
}

// netperfCmd deliberately omits the global "-P 0" (banner off) flag: on this
// netperf build (2.7.0) it also suppresses the -o CSV column-header line,
// not just the "MIGRATED ..." banner text, breaking parseNetperfCSV's
// two-line (header, values) contract. Leaving the banner in is harmless —
// parseNetperfCSV only looks at the last two non-empty lines.
func netperfCmd(target benchTarget, seconds int) string {
	return fmt.Sprintf("netperf -H %s -p %s -t TCP_RR -l %d -- -r 64,64 -P %s,%s -o THROUGHPUT,MEAN_LATENCY,P50_LATENCY,P99_LATENCY",
		target.host, target.netserver, seconds, netperfLocalPort, netperfDataPort)
}

func applyNetem(t *testing.T, c benchCondition) {
	t.Helper()
	if c.netem == "" {
		return
	}
	for _, svc := range []string{"client", "server"} {
		execIn(t, svc, "tc qdisc replace dev eth0 root netem "+c.netem)
		// Prove the qdisc landed on this side too (Step 5 of Task 4 checks
		// the RTT effect via TCP_RR) — a one-sided netem would silently
		// halve the intended delay/loss.
		out := execIn(t, svc, "tc qdisc show dev eth0")
		if !strings.Contains(out, "netem") {
			fatalf(t, "netem not applied on %s eth0:\n%s", svc, out)
		}
	}
}

func clearNetem(t *testing.T) {
	t.Helper()
	for _, svc := range []string{"client", "server"} {
		if out, err := execInOK(svc, "tc qdisc del dev eth0 root 2>&1 || true"); err != nil {
			t.Logf("clearNetem(%s): %v\n%s", svc, err, out)
		}
	}
}

// collectBenchDiagnostics records tc/wg state for a just-finished WAN cell —
// called after runInstruments, before clearNetem removes the qdisc — so a
// surprising result (in particular the WireGuard row) can be reconciled
// against the netem qdisc's own drop/overlimit counters instead of taken on
// faith. For wireguard it also captures wg0's MTU and transfer counters.
func collectBenchDiagnostics(t *testing.T, res *benchResults, tr, cond string) {
	t.Helper()
	record := func(name, out string) {
		t.Logf("=== diagnostics %s/%s — %s:\n%s", tr, cond, name, out)
		res.Diagnostics = append(res.Diagnostics, benchDiag{Transport: tr, Condition: cond, Name: name, Output: strings.TrimSpace(out)})
	}
	for _, svc := range []string{"client", "server"} {
		out := strings.TrimSpace(execIn(t, svc, "tc -s qdisc show dev eth0"))
		// Append a computed drop ratio so fairness is auditable straight
		// from the report, without making the benchmark fail over a
		// diagnostics parse (tc's output format is not a stable contract).
		if dropped, sent, ratio, err := parseNetemDropRatio(out); err != nil {
			out += fmt.Sprintf("\ndrop ratio: unparsed (%v)", err)
		} else {
			out += fmt.Sprintf("\ndrop ratio: %d/(%d+%d) = %.3f %%", dropped, sent, dropped, ratio)
		}
		record(svc+" tc -s qdisc show dev eth0", out)
	}
	if tr == trWG {
		record("client ip -d link show wg0", execIn(t, "client", "ip -d link show wg0"))
		record("client wg show wg0 transfer", execIn(t, "client", "wg show wg0 transfer"))
	}
}

// timedExec runs cmd in the client container, timing it inside the
// container (docker exec startup is excluded). Tool output goes to
// benchOut and is returned; a non-zero tool exit fails the test.
// timedScript wraps cmd so a single execIn/execInOK call recovers both its
// wall time (measured inside the container, excluding docker-exec startup)
// and its exit code: tool output is captured to benchOut and a trailing
// BENCH_T line reports start/end epoch seconds and rc. Shared by timedExec
// and detectScpForm's probe so a scp form is only accepted if it behaves the
// same way under the exact framing it will actually be measured with.
func timedScript(cmd string) string {
	return `s=$(date +%s.%N); ` + cmd + ` > ` + benchOut + ` 2>&1; rc=$?; e=$(date +%s.%N); ` +
		`echo "BENCH_T $s $e $rc"; cat ` + benchOut
}

// parseBenchT extracts the BENCH_T line timedScript appends. ok is false
// when raw has no such line (e.g. the shell itself died before echoing it).
func parseBenchT(raw string) (wallSec float64, rc string, out string, ok bool) {
	m := regexp.MustCompile(`BENCH_T (\S+) (\S+) (\d+)\n?`).FindStringSubmatchIndex(raw)
	if m == nil {
		return 0, "", raw, false
	}
	start, _ := strconv.ParseFloat(raw[m[2]:m[3]], 64)
	end, _ := strconv.ParseFloat(raw[m[4]:m[5]], 64)
	return end - start, raw[m[6]:m[7]], raw[m[1]:], true
}

func timedExec(t *testing.T, cmd string) (wallSec float64, out string) {
	t.Helper()
	raw := execIn(t, "client", timedScript(cmd))
	wall, rc, out, ok := parseBenchT(raw)
	if !ok {
		fatalf(t, "timedExec: no BENCH_T line in output of %q:\n%s", cmd, raw)
	}
	if rc != "0" {
		fatalf(t, "timedExec: %q exited %s:\n%s", cmd, rc, out)
	}
	return wall, out
}

type benchSnapshot struct {
	host cpuTimes
	cont map[string]uint64
	rx   uint64
}

// snapshot is the "before" snapshot: container CPU and rx bytes (each a
// docker-exec round trip) are read FIRST and hostCPU LAST, so their own CPU
// cost lands before the timed window starts rather than inside it.
func snapshot(t *testing.T) benchSnapshot {
	t.Helper()
	s := benchSnapshot{cont: map[string]uint64{}}
	for _, svc := range []string{"client", "server", "relay"} {
		s.cont[svc] = containerCPUUsec(t, svc)
	}
	s.rx = ifaceRxBytes(t, "client")
	s.host = hostCPU(t)
	return s
}

// snapshotContainers fills in the container CPU / rx fields of an
// already-created "after" snapshot. Used by measure, which must read
// hostCPU immediately when timedExec returns — before these docker-exec
// calls run — so their CPU cost lands after the timed window instead of
// inside it.
func snapshotContainers(t *testing.T, s *benchSnapshot) {
	t.Helper()
	s.cont = map[string]uint64{}
	for _, svc := range []string{"client", "server", "relay"} {
		s.cont[svc] = containerCPUUsec(t, svc)
	}
	s.rx = ifaceRxBytes(t, "client")
}

// measure runs one timed command between two snapshots and returns a sample
// with the footprint deltas filled in (Value/latency fields are set by the
// caller from out).
func measure(t *testing.T, cmd string) (benchSample, string) {
	t.Helper()
	before := snapshot(t)
	wall, out := timedExec(t, cmd)
	after := benchSnapshot{host: hostCPU(t)}
	snapshotContainers(t, &after)
	s := benchSample{
		WallSec:         wall,
		HostCPUSec:      after.host.busySecondsSince(before.host),
		WireBytes:       after.rx - before.rx,
		ContainerCPUSec: map[string]float64{},
	}
	for svc := range before.cont {
		s.ContainerCPUSec[svc] = float64(after.cont[svc]-before.cont[svc]) / 1e6
	}
	return s, out
}

func runInstruments(t *testing.T, cfg benchCfg, res *benchResults, tr, cond string, target benchTarget, payloads benchPayloads) {
	t.Helper()
	form := res.Env.ScpForm

	// WAN scp is loss-limited, not bandwidth-limited: a single TCP flow
	// under netem loss+RTT is capped by the Mathis limit (a function of
	// RTT/loss, not of anything a warm-up would improve), so the 5 GiB LAN
	// file would take ~45 min per run for no extra signal. Use the smaller
	// warm file's own size as the WAN payload instead — this changes the
	// payload size being measured, not the transport under test.
	scpFile, payload := benchFile, payloads.full
	if cond == condWAN {
		scpFile, payload = benchWarmFile, payloads.warm
	}

	t.Log("warm-up (untimed)")
	timedExec(t, iperfCmd(target, 1, 2))
	timedExec(t, iperfCmd(target, 4, 2))
	timedExec(t, netperfCmd(target, 2))
	if cond != condWAN {
		// Skip the scp warm-up on WAN: the file is already resident in the
		// server's tmpfs page cache from benchFixture, and at ~2 MB/s under
		// loss a 256 MiB warm-up would cost ~2 minutes per transport for
		// nothing (loss response, not caching, is what WAN measures).
		timedExec(t, scpCmd(form, target, benchWarmFile))
	}

	tag := func(s benchSample, inst string, run int) benchSample {
		s.Transport, s.Condition, s.Instrument, s.Run = tr, cond, inst, run
		return s
	}
	for run := 1; run <= cfg.runs; run++ {
		for _, streams := range []int{1, 4} {
			s, out := measure(t, iperfCmd(target, streams, iperfSeconds))
			bps, err := parseIperfJSON([]byte(out))
			if err != nil {
				fatalf(t, "%s/%s iperf3 -P %d run %d: %v\n%s", tr, cond, streams, run, err, out)
			}
			s.Value = bps / 1e9
			inst := instIperf1
			if streams == 4 {
				inst = instIperf4
			}
			s.WireBytes = 0 // wire overhead is reported from the scp runs only
			res.add(tag(s, inst, run))
			t.Logf("%s/%s %s run %d: %.2f Gbit/s in %.1fs, host CPU %.1fs", tr, cond, inst, run, s.Value, s.WallSec, s.HostCPUSec)
		}

		s, out := measure(t, netperfCmd(target, netperfSeconds))
		rr, err := parseNetperfCSV(out)
		if err != nil {
			fatalf(t, "%s/%s netperf run %d: %v", tr, cond, run, err)
		}
		s.Value, s.MeanUs, s.P50Us, s.P99Us = rr.TransPerSec, rr.MeanUs, rr.P50Us, rr.P99Us
		s.WireBytes = 0
		res.add(tag(s, instRR, run))
		t.Logf("%s/%s tcp_rr run %d: %.0f trans/s, p50 %.0f µs, p99 %.0f µs", tr, cond, run, rr.TransPerSec, rr.P50Us, rr.P99Us)

		s, _ = measure(t, scpCmd(form, target, scpFile))
		s.PayloadBytes = payload
		s.Value = float64(payload) / s.WallSec / 1e6
		res.add(tag(s, instScp, run))
		t.Logf("%s/%s scp run %d: %.0f MB/s in %.1fs, wire %.2f%% over payload, CPU host %.1fs client %.1fs server %.1fs relay %.1fs",
			tr, cond, run, s.Value, s.WallSec, (float64(s.WireBytes)-float64(payload))/float64(payload)*100,
			s.HostCPUSec, s.ContainerCPUSec["client"], s.ContainerCPUSec["server"], s.ContainerCPUSec["relay"])
	}
}

func benchTransports() []benchTransport {
	return []benchTransport{
		{
			name: trSSH,
			setup: func(t *testing.T) benchTarget {
				return benchTarget{host: "server", ssh: sshdPort, iperf: iperfPort, netserver: netserverPort}
			},
			teardown: func(*testing.T) {},
			// The processes common to every transport, reported once as the baseline.
			rssProcs: map[string]string{
				"server / sshd":      "/usr/sbin/sshd",
				"server / iperf3":    "iperf3 -s",
				"server / netserver": "netserver",
			},
		},
		{
			name:     trWG,
			setup:    wgSetup,
			teardown: wgTeardown,
			rssProcs: map[string]string{}, // kernel WireGuard: no userspace process
		},
		{
			name:     trTW,
			setup:    twSetup,
			teardown: twTeardown,
			rssProcs: map[string]string{
				"client / tw client connect": "tw client connect",
				"server / tw server start":   "tw server start",
				"relay / xray":               "xray run",
				"relay / caddy":              "caddy run",
			},
		},
	}
}

// wgSetup brings up one direct kernel-WireGuard link client ↔ server with
// plain `ip`/`wg` (no wg-quick: no resolvconf/iptables side effects).
func wgSetup(t *testing.T) benchTarget {
	t.Helper()
	t.Cleanup(func() { wgTeardown(t) })
	execIn(t, "server", `set -e; umask 077; wg genkey > /bench/wg.key; wg pubkey < /bench/wg.key > /shared/bench-wg-server.pub; `+
		`ip link del wg0 2>/dev/null || true; ip link add wg0 type wireguard; `+
		`wg set wg0 listen-port `+wgPort+` private-key /bench/wg.key; ip addr add `+wgServerIP+`/24 dev wg0; ip link set wg0 up`)
	execIn(t, "client", `set -e; umask 077; wg genkey > /bench/wg.key; wg pubkey < /bench/wg.key > /shared/bench-wg-client.pub; `+
		`ip link del wg0 2>/dev/null || true; ip link add wg0 type wireguard; `+
		`wg set wg0 private-key /bench/wg.key; ip addr add `+wgClientIP+`/24 dev wg0; ip link set wg0 up`)
	execIn(t, "server", `wg set wg0 peer "$(cat /shared/bench-wg-client.pub)" allowed-ips `+wgClientIP+`/32`)
	execIn(t, "client", `wg set wg0 peer "$(cat /shared/bench-wg-server.pub)" `+
		`endpoint "$(getent hosts server | awk '{print $1}' | head -1):`+wgPort+`" allowed-ips `+wgServerIP+`/32 persistent-keepalive 25`)
	waitFor(t, "wireguard handshake (sshd reachable over wg0)", 30*time.Second, func() (bool, string) {
		_, err := execInOK("client", "nc -z -w 2 "+wgServerIP+" "+sshdPort)
		if err != nil {
			out, _ := execInOK("client", "wg show wg0")
			return false, out
		}
		return true, ""
	})
	t.Log(execIn(t, "client", "ip -d link show wg0"))
	t.Log(execIn(t, "client", "wg show wg0"))
	return benchTarget{host: wgServerIP, ssh: sshdPort, iperf: iperfPort, netserver: netserverPort}
}

func wgTeardown(t *testing.T) {
	t.Helper()
	for _, svc := range []string{"client", "server"} {
		if out, err := execInOK(svc, "ip link del wg0 2>&1 || true; rm -f /bench/wg.key"); err != nil {
			t.Logf("wgTeardown(%s): %v\n%s", svc, err, out)
		}
	}
	execInOK("server", "rm -f /shared/bench-wg-server.pub /shared/bench-wg-client.pub")
}

// twSetup invites the bench user with the four port mappings, joins on the
// client, and starts the real client tunnel — exactly what a tw user does.
func twSetup(t *testing.T) benchTarget {
	t.Helper()
	t.Cleanup(func() { twTeardown(t) })
	killMatching(t, "client", "tw client connect")
	execIn(t, "client", "rm -rf /etc/tw-test")
	if out, err := execInOK("server", "printf 'y\\n' | tw server user delete "+benchUser); err == nil {
		t.Logf("deleted a leftover %s user from a previous run:\n%s", benchUser, out)
	}
	invite := fmt.Sprintf("tw server user invite %s -m %s:%s -m %s:%s -m %s:%s -m %s:%s",
		benchUser, twSSHPort, sshdPort, twIperfPort, iperfPort, twNetserverPort, netserverPort, netperfDataPort, netperfDataPort)
	_, joinLog := runInviteExchange(t, "server", invite, "client", "tw join "+domain+" {code}")
	if !strings.Contains(joinLog, `Context "`+benchUser+`" (client) created.`) {
		fatalf(t, "tw join did not create the %s context:\n%s", benchUser, joinLog)
	}
	execDetached(t, "client", "tw client connect > /var/log/tw-bench-client.log 2>&1")
	waitFor(t, "tw client tunnel listening on :"+twSSHPort, 120*time.Second, func() (bool, string) {
		if _, err := execInOK("client", "nc -z 127.0.0.1 "+twSSHPort); err != nil {
			out, _ := execInOK("client", "tail -5 /var/log/tw-bench-client.log")
			return false, out
		}
		return true, ""
	})
	return benchTarget{host: "127.0.0.1", ssh: twSSHPort, iperf: twIperfPort, netserver: twNetserverPort}
}

func twTeardown(t *testing.T) {
	t.Helper()
	killMatching(t, "client", "tw client connect")
	if out, err := execInOK("server", "printf 'y\\n' | tw server user delete "+benchUser); err != nil {
		t.Logf("twTeardown: user delete: %v\n%s", err, out)
	}
}
