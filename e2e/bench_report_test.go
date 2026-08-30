//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSummarize(t *testing.T) {
	s := summarize([]float64{3, 1, 2})
	if s.Med != 2 || s.Min != 1 || s.Max != 3 || s.N != 3 {
		t.Fatalf("odd: %+v", s)
	}
	s = summarize([]float64{4, 1, 3, 2})
	if s.Med != 2.5 || s.Min != 1 || s.Max != 4 {
		t.Fatalf("even: %+v", s)
	}
	if s := summarize(nil); s.N != 0 {
		t.Fatalf("empty: %+v", s)
	}
}

func sampleResults() *benchResults {
	r := &benchResults{Env: benchEnv{CPUModel: "TestCPU", Cores: 8, Runs: 2, SizeGB: 1, WAN: "30ms:0.1%"}}
	r.Conditions = []benchConditionInfo{
		{Name: condLAN, Label: "LAN (0 ms RTT, no loss)", ScpPayloadBytes: 1 << 30},       // 1 GiB
		{Name: condWAN, Label: "WAN (30 ms RTT, 0.1 % loss)", ScpPayloadBytes: 256 << 20}, // 256 MiB
	}
	cc := map[string]float64{"client": 1, "server": 2, "relay": 0}
	for _, tr := range benchTransportOrder {
		for run := 1; run <= 2; run++ {
			r.add(benchSample{Transport: tr, Condition: condLAN, Instrument: instIperf1, Run: run, WallSec: 10, Value: 2.0 + float64(run), HostCPUSec: 20, ContainerCPUSec: cc})
			r.add(benchSample{Transport: tr, Condition: condLAN, Instrument: instIperf4, Run: run, WallSec: 10, Value: 5.0, HostCPUSec: 30, ContainerCPUSec: cc})
			r.add(benchSample{Transport: tr, Condition: condLAN, Instrument: instRR, Run: run, WallSec: 10, Value: 30000, MeanUs: 33, P50Us: 30, P99Us: 90, HostCPUSec: 5, ContainerCPUSec: cc})
			r.add(benchSample{Transport: tr, Condition: condLAN, Instrument: instScp, Run: run, WallSec: 8, Value: 125, PayloadBytes: 1_000_000_000, WireBytes: 1_050_000_000, HostCPUSec: 16, ContainerCPUSec: cc})
		}
	}
	r.RSS = []benchRSS{
		{Transport: trSSH, Label: "Baseline (all transports; idle listeners after the runs)", PeakKB: map[string]uint64{"server / sshd": 4096}},
		{Transport: trTW, PeakKB: map[string]uint64{"client / tw client connect": 40960, "relay / xray": 30720}},
	}
	r.Diagnostics = []benchDiag{
		{Transport: trWG, Condition: condWAN, Name: "client ip -d link show wg0", Output: "mtu 1420 qdisc noqueue"},
	}
	return r
}

func TestRenderBenchMarkdown(t *testing.T) {
	md := renderBenchMarkdown(sampleResults())
	for _, want := range []string{
		"## Results — LAN (0 ms RTT, no loss)",
		"| Pure SSH |", "| SSH over WireGuard |", "| SSH over Tunnel Whisperer |",
		"3.50 (3.00–4.00)",    // iperf3-1 median of 3,4 with min–max
		"30000 (30000–30000)", // tcp_rr trans/s
		"30 / 90",             // p50 / p99 µs
		"125 (125–125)",       // scp MB/s
		"5.0 %",               // wire overhead: 1.05e9 vs 1e9
		"1.75",                // Gbit/s per busy core: 3.5 / (20/10)
		"1.0 / 2.0 / 0.0",     // container CPU-s split client/server/relay for scp
		"scp 1 GiB (MB/s)",    // LAN scp column header uses the LAN payload size
		"NIC offloads",        // Environment table row present (round 4: tso/gso/gro off)
		"## Memory footprint",
		"| Baseline (all transports; idle listeners after the runs) | server / sshd | 4.0 MiB |", // Label overrides benchTransportLabel
		"| SSH over Tunnel Whisperer | client / tw client connect | 40.0 MiB |",
		"## Diagnostics",
		"### wireguard / wan — client ip -d link show wg0",
		"```\nmtu 1420 qdisc noqueue\n```",
		"TestCPU",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
	// WAN section listed even though no WAN samples exist: cells must be "—".
	if !strings.Contains(md, "## Results — WAN (30 ms RTT, 0.1 % loss)") || !strings.Contains(md, "| Pure SSH | — |") {
		t.Fatalf("WAN section without samples must render dashes:\n%s", md)
	}
	// WAN scp column header uses the WAN (warm-file) payload size, not the LAN one.
	if !strings.Contains(md, "scp 256 MiB (MB/s)") {
		t.Fatalf("WAN scp header missing the 256 MiB payload size:\n%s", md)
	}
}

func TestBenchResultsJSONRoundTrip(t *testing.T) {
	r := sampleResults()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var back benchResults
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Samples) != len(r.Samples) || back.Env.CPUModel != "TestCPU" || back.RSS[1].PeakKB["relay / xray"] != 30720 ||
		back.RSS[0].Label != "Baseline (all transports; idle listeners after the runs)" ||
		back.Diagnostics[0].Output != "mtu 1420 qdisc noqueue" {
		t.Fatalf("round trip lost data: %+v", back)
	}
}

var benchSeriesColorRe = regexp.MustCompile(`<text[^>]*fill="(#2a78d6|#eb6834|#1baf7a|#3987e5|#d95926|#199e70)"`)

func TestRenderBenchSVG(t *testing.T) {
	r := sampleResults()
	for _, dark := range []bool{false, true} {
		svg := renderBenchSVG(r, dark)
		if !strings.HasPrefix(svg, "<svg") {
			t.Fatalf("dark=%v: output does not start with <svg: %.80s", dark, svg)
		}
		for _, want := range []string{
			"<title>Tunnel Whisperer benchmark",
			">Pure SSH<", ">SSH over WireGuard<", ">SSH over Tunnel Whisperer<",
			">LAN throughput<", ">LAN file copy<", ">LAN latency<", ">Lossy WAN, parallel streams<", ">CPU per 5 GiB copy<",
			">3.50<", // iperf3-1 median of 3,4
			">125<",  // scp median
			">30<",   // tcp_rr p50 median
			">3<",    // CPU total: client 1 + server 2 + relay 0
		} {
			if !strings.Contains(svg, want) {
				t.Fatalf("dark=%v: missing %q", dark, want)
			}
		}
		surface := "#fcfcfb"
		if dark {
			surface = "#1a1a19"
		}
		if !strings.Contains(svg, surface) {
			t.Fatalf("dark=%v: missing surface colour %s", dark, surface)
		}
		// The iperf3-1 bar (min 3, max 4) draws a whisker (main + 2 caps)
		// per transport; every other bar in the fixture has min == max and
		// draws none, and the WAN panel has no samples at all.
		if n := strings.Count(svg, `class="whisker"`); n != 3*3 {
			t.Fatalf("dark=%v: expected 9 whisker lines, got %d", dark, n)
		}
		if benchSeriesColorRe.MatchString(svg) {
			t.Fatalf("dark=%v: a <text> element uses a series colour as fill", dark)
		}
	}
}

// TestBenchChartUpToDate is a drift guard, not a generator: it renders the
// committed real-run e2e/bench-results.json with the current code and
// checks the result matches the committed SVGs byte-for-byte — in e2e/ and
// in their docs/assets/ copies. Skips when the JSON isn't present (e.g. a
// fresh checkout that hasn't run `make bench` yet). This runs as part of
// the package's ordinary -tags e2e test suite (including under `make e2e`,
// which only skips TestBench itself), so it must never write files on a
// plain run — only BENCH_WRITE_CHART=1 does that, and only to e2e/, never
// to docs/assets/ (copy those with `make bench` or the command below).
func TestBenchChartUpToDate(t *testing.T) {
	data, err := os.ReadFile(benchResultsFile)
	if err != nil {
		t.Skip("no bench-results.json present, skipping chart drift check")
	}
	var r benchResults
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("unmarshal %s: %v", benchResultsFile, err)
	}
	light := []byte(renderBenchSVG(&r, false))
	dark := []byte(renderBenchSVG(&r, true))

	if os.Getenv("BENCH_WRITE_CHART") == "1" {
		if err := os.WriteFile(benchChartFile, light, 0o644); err != nil {
			t.Fatalf("write %s: %v", benchChartFile, err)
		}
		if err := os.WriteFile(benchChartDarkFile, dark, 0o644); err != nil {
			t.Fatalf("write %s: %v", benchChartDarkFile, err)
		}
		return
	}

	const fix = "run: cd e2e && BENCH_WRITE_CHART=1 go test -tags e2e -run TestBenchChartUpToDate . && cp bench-chart*.svg ../docs/assets/ (or `make bench`)"
	checks := []struct {
		path string
		want []byte
	}{
		{benchChartFile, light},
		{benchChartDarkFile, dark},
		{filepath.Join("..", "docs", "assets", benchChartFile), light},
		{filepath.Join("..", "docs", "assets", benchChartDarkFile), dark},
	}
	var stale []string
	for _, c := range checks {
		got, err := os.ReadFile(c.path)
		if err != nil || !bytes.Equal(got, c.want) {
			stale = append(stale, c.path)
		}
	}
	if len(stale) > 0 {
		t.Fatalf("stale chart file(s), rendered output no longer matches: %s\n%s", strings.Join(stale, ", "), fix)
	}
}

var benchStackSegRe = regexp.MustCompile(`<path class="seg (seg-client|seg-server|seg-relay)" d="([^"]*)"`)

// TestRenderStackPanelSkipsSubPixelSegment covers three cases in the CPU
// stack panel at once: a genuinely zero segment (wireguard's relay), a
// non-zero but sub-pixel segment from cgroup CPU-accounting noise (ssh's
// relay ≈0.004s), and a real, visible segment (tw's relay). Only the last
// should render, and in both other cases the rounded top must land on the
// real topmost segment (server), not get stolen by a skipped one.
func TestRenderStackPanelSkipsSubPixelSegment(t *testing.T) {
	r := &benchResults{}
	r.Conditions = []benchConditionInfo{{Name: condLAN, Label: "LAN", ScpPayloadBytes: 1 << 30}}
	r.add(benchSample{Transport: trSSH, Condition: condLAN, Instrument: instScp, Run: 1, Value: 100,
		ContainerCPUSec: map[string]float64{"client": 10, "server": 90, "relay": 0.004}})
	r.add(benchSample{Transport: trWG, Condition: condLAN, Instrument: instScp, Run: 1, Value: 100,
		ContainerCPUSec: map[string]float64{"client": 20, "server": 80, "relay": 0}})
	r.add(benchSample{Transport: trTW, Condition: condLAN, Instrument: instScp, Run: 1, Value: 100,
		ContainerCPUSec: map[string]float64{"client": 90, "server": 80, "relay": 220}})

	svg := renderBenchSVG(r, false)
	matches := benchStackSegRe.FindAllStringSubmatch(svg, -1)
	if len(matches) != 7 {
		var classes []string
		for _, m := range matches {
			classes = append(classes, m[1])
		}
		t.Fatalf("expected 7 segments (ssh: client+server, wg: client+server, tw: client+server+relay), got %d: %v", len(matches), classes)
	}

	rounded := func(d string) bool { return strings.Contains(d, "Q") }
	want := []struct {
		class   string
		rounded bool
	}{
		{"seg-client", false}, {"seg-server", true}, // ssh: relay (0.004s, sub-pixel) skipped — server is topmost
		{"seg-client", false}, {"seg-server", true}, // wg: relay (0) skipped — server is topmost
		{"seg-client", false}, {"seg-server", false}, {"seg-relay", true}, // tw: relay is real and topmost
	}
	for i, w := range want {
		if matches[i][1] != w.class {
			t.Fatalf("segment %d: want class %q, got %q", i, w.class, matches[i][1])
		}
		if got := rounded(matches[i][2]); got != w.rounded {
			t.Fatalf("segment %d (%s): want rounded=%v, got=%v\nd=%s", i, w.class, w.rounded, got, matches[i][2])
		}
	}
}
