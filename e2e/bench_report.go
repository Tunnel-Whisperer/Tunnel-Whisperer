//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	benchReportFile  = "bench-report.md"
	benchResultsFile = "bench-results.json"

	trSSH = "ssh"
	trWG  = "wireguard"
	trTW  = "tw"

	condLAN = "lan"
	condWAN = "wan"

	instIperf1 = "iperf3-1"
	instIperf4 = "iperf3-4"
	instRR     = "tcp_rr"
	instScp    = "scp"
)

var benchTransportOrder = []string{trSSH, trWG, trTW}

var benchTransportLabel = map[string]string{
	trSSH: "Pure SSH",
	trWG:  "SSH over WireGuard",
	trTW:  "SSH over Tunnel Whisperer",
}

// benchEnv is the reproducibility header. Only container-side and generic
// host facts — never hostnames, usernames or host paths.
type benchEnv struct {
	Generated  string `json:"generated"`
	CPUModel   string `json:"cpu_model"`
	Cores      int    `json:"cores"`
	Kernel     string `json:"kernel"`
	Docker     string `json:"docker"`
	TW         string `json:"tw"`
	OpenSSH    string `json:"openssh"`
	Iperf3     string `json:"iperf3"`
	Netperf    string `json:"netperf"`
	WireGuard  string `json:"wireguard"`
	Image      string `json:"image"`
	WAN        string `json:"wan"`
	Offloads   string `json:"offloads"`
	ScpForm    string `json:"scp_form"`
	ScpOptions string `json:"scp_options"`
	Runs       int    `json:"runs"`
	SizeGB     int    `json:"size_gb"`
}

// benchSample is one timed run of one instrument in one cell.
type benchSample struct {
	Transport       string             `json:"transport"`
	Condition       string             `json:"condition"`
	Instrument      string             `json:"instrument"`
	Run             int                `json:"run"`
	WallSec         float64            `json:"wall_sec"`
	Value           float64            `json:"value"` // Gbit/s (iperf3), trans/s (tcp_rr), MB/s (scp)
	MeanUs          float64            `json:"mean_us,omitempty"`
	P50Us           float64            `json:"p50_us,omitempty"`
	P99Us           float64            `json:"p99_us,omitempty"`
	PayloadBytes    uint64             `json:"payload_bytes,omitempty"`
	WireBytes       uint64             `json:"wire_bytes,omitempty"`
	HostCPUSec      float64            `json:"host_cpu_sec"`
	ContainerCPUSec map[string]float64 `json:"container_cpu_sec"` // client / server / relay
}

// benchRSS is the peak RSS (kB) of each transport process at the end of
// that transport's runs, keyed "<service> / <label>". Label overrides the
// row's display name in the Markdown report (falls back to
// benchTransportLabel[Transport] when empty) — used to mark the ssh
// transport's rows as the shared baseline rather than "Pure SSH", since
// sshd/iperf3/netserver are common to every transport, not specific to ssh.
type benchRSS struct {
	Transport string            `json:"transport"`
	Label     string            `json:"label,omitempty"`
	PeakKB    map[string]uint64 `json:"peak_kb"`
}

type benchConditionInfo struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	// ScpPayloadBytes is the size of the file the scp instrument actually
	// transferred in this condition (see bench_test.go's runInstruments: WAN
	// uses the smaller warm file — a WAN scp is loss-limited, not
	// bandwidth-limited, so the 5 GiB LAN file would take ~45 min per run
	// for no extra signal). Shown in the per-condition table's scp header.
	ScpPayloadBytes uint64 `json:"scp_payload_bytes"`
}

// benchDiag is one piece of raw diagnostic output captured for a WAN cell
// (tc/wg state), so a surprising result — e.g. the WireGuard row — can be
// reconciled against the qdisc drop counters and wg0's own MTU/transfer
// stats instead of taken on faith.
type benchDiag struct {
	Transport string `json:"transport"`
	Condition string `json:"condition"`
	Name      string `json:"name"`
	Output    string `json:"output"`
}

type benchResults struct {
	Env         benchEnv             `json:"env"`
	Conditions  []benchConditionInfo `json:"conditions"`
	Samples     []benchSample        `json:"samples"`
	RSS         []benchRSS           `json:"rss"`
	Diagnostics []benchDiag          `json:"diagnostics"`
}

func (r *benchResults) add(s benchSample) { r.Samples = append(r.Samples, s) }

func (r *benchResults) cell(tr, cond, inst string) []benchSample {
	var out []benchSample
	for _, s := range r.Samples {
		if s.Transport == tr && s.Condition == cond && s.Instrument == inst {
			out = append(out, s)
		}
	}
	return out
}

func (r *benchResults) values(tr, cond, inst string, f func(benchSample) float64) []float64 {
	var out []float64
	for _, s := range r.cell(tr, cond, inst) {
		out = append(out, f(s))
	}
	return out
}

type benchStat struct {
	Med, Min, Max float64
	N             int
}

func summarize(xs []float64) benchStat {
	if len(xs) == 0 {
		return benchStat{}
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	med := s[n/2]
	if n%2 == 0 {
		med = (s[n/2-1] + s[n/2]) / 2
	}
	return benchStat{Med: med, Min: s[0], Max: s[n-1], N: n}
}

// fmtStat renders "med (min–max)" with prec decimals, or "—" for no data.
func fmtStat(s benchStat, prec int) string {
	if s.N == 0 {
		return "—"
	}
	return fmt.Sprintf("%.*f (%.*f–%.*f)", prec, s.Med, prec, s.Min, prec, s.Max)
}

func fmtMed(s benchStat, prec int) string {
	if s.N == 0 {
		return "—"
	}
	return fmt.Sprintf("%.*f", prec, s.Med)
}

func value(s benchSample) float64 { return s.Value }

// fmtScpPayload renders a byte count as the whole-number GiB/MiB label used
// in the per-condition scp column header.
func fmtScpPayload(n uint64) string {
	const mib = 1024 * 1024
	const gib = 1024 * mib
	if n >= gib {
		return fmt.Sprintf("%.0f GiB", float64(n)/gib)
	}
	return fmt.Sprintf("%.0f MiB", float64(n)/mib)
}

func renderBenchMarkdown(r *benchResults) string {
	var b strings.Builder
	e := r.Env
	fmt.Fprintf(&b, "# Tunnel Whisperer throughput & footprint benchmark\n\n")
	fmt.Fprintf(&b, "Generated: %s\n\n", e.Generated)
	fmt.Fprintf(&b, "Three transports, same workload: the client pulls data from the server over plain SSH, over SSH inside a direct kernel-WireGuard link, and over SSH inside the real Tunnel Whisperer path (`tw client connect` → VLESS/XHTTP/mTLS :443 → relay Caddy + Xray → reverse SSH → `tw serve` → sshd). Every instrument moves bytes server → client.\n\n")

	fmt.Fprintf(&b, "## Environment\n\n| Item | Value |\n|------|-------|\n")
	for _, kv := range [][2]string{
		{"CPU", fmt.Sprintf("%s (%d logical cores)", e.CPUModel, e.Cores)},
		{"Host kernel", e.Kernel},
		{"Docker", e.Docker},
		{"Container image", e.Image},
		{"tw", e.TW},
		{"OpenSSH", e.OpenSSH},
		{"iperf3", e.Iperf3},
		{"netperf", e.Netperf},
		{"WireGuard", e.WireGuard},
		{"WAN emulation", e.WAN},
		{"NIC offloads", e.Offloads},
		{"scp form / options", e.ScpForm + " · `" + e.ScpOptions + "`"},
		{"Runs per cell", fmt.Sprintf("%d timed (after 1 warm-up); cells show median (min–max)", e.Runs)},
		{"scp payload", fmt.Sprintf("%d GiB of /dev/urandom on LAN, the 256 MiB warm-up file on WAN (TCP loss-bound ≈2 MB/s makes 5 GiB impractical); served from tmpfs, written to /dev/null", e.SizeGB)},
	} {
		fmt.Fprintf(&b, "| %s | %s |\n", kv[0], strings.ReplaceAll(kv[1], "|", "\\|"))
	}

	for _, c := range r.Conditions {
		fmt.Fprintf(&b, "\n## Results — %s\n\n", c.Label)
		fmt.Fprintf(&b, "| Transport | iperf3 1 stream (Gbit/s) | iperf3 4 streams (Gbit/s) | TCP_RR (trans/s) | TCP_RR p50 / p99 (µs) | scp %s (MB/s) | scp wall (s) | wire overhead | Gbit/s per busy core | CPU-s per scp run client / server / relay |\n",
			fmtScpPayload(c.ScpPayloadBytes))
		fmt.Fprintln(&b, "|---|---|---|---|---|---|---|---|---|---|")
		for _, tr := range benchTransportOrder {
			ip1 := summarize(r.values(tr, c.Name, instIperf1, value))
			ip4 := summarize(r.values(tr, c.Name, instIperf4, value))
			rr := summarize(r.values(tr, c.Name, instRR, value))
			p50 := summarize(r.values(tr, c.Name, instRR, func(s benchSample) float64 { return s.P50Us }))
			p99 := summarize(r.values(tr, c.Name, instRR, func(s benchSample) float64 { return s.P99Us }))
			scp := summarize(r.values(tr, c.Name, instScp, value))
			wall := summarize(r.values(tr, c.Name, instScp, func(s benchSample) float64 { return s.WallSec }))
			over := summarize(r.values(tr, c.Name, instScp, func(s benchSample) float64 {
				if s.PayloadBytes == 0 {
					return 0
				}
				return (float64(s.WireBytes) - float64(s.PayloadBytes)) / float64(s.PayloadBytes) * 100
			}))
			perCore := summarize(r.values(tr, c.Name, instIperf1, func(s benchSample) float64 {
				if s.HostCPUSec == 0 || s.WallSec == 0 {
					return 0
				}
				return s.Value / (s.HostCPUSec / s.WallSec)
			}))
			split := "—"
			if cl, sv, rl := summarize(r.values(tr, c.Name, instScp, func(s benchSample) float64 { return s.ContainerCPUSec["client"] })),
				summarize(r.values(tr, c.Name, instScp, func(s benchSample) float64 { return s.ContainerCPUSec["server"] })),
				summarize(r.values(tr, c.Name, instScp, func(s benchSample) float64 { return s.ContainerCPUSec["relay"] })); cl.N > 0 {
				split = fmt.Sprintf("%s / %s / %s", fmtMed(cl, 1), fmtMed(sv, 1), fmtMed(rl, 1))
			}
			rrLat := "—"
			if p50.N > 0 {
				rrLat = fmt.Sprintf("%s / %s", fmtMed(p50, 0), fmtMed(p99, 0))
			}
			overS := "—"
			if over.N > 0 {
				overS = fmt.Sprintf("%.1f %%", over.Med)
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
				benchTransportLabel[tr], fmtStat(ip1, 2), fmtStat(ip4, 2), fmtStat(rr, 0), rrLat,
				fmtStat(scp, 0), fmtMed(wall, 1), overS, fmtMed(perCore, 2), split)
		}
	}

	fmt.Fprintf(&b, "\n## Memory footprint\n\nPeak resident set (VmHWM) of the transport processes. The baseline rows are idle listeners sampled after the SSH transport's runs (the first transport) closed their connections; VmHWM is a since-start high-water mark, so a higher peak reached later by these shared listeners is not captured. The tw and WireGuard rows are transfer-time peaks sampled at the end of that transport's own runs. WireGuard has no userspace process: its footprint is kernel memory, reported as 0.\n\n")
	fmt.Fprintln(&b, "| Transport | Process | Peak RSS |")
	fmt.Fprintln(&b, "|---|---|---|")
	for _, rs := range r.RSS {
		label := rs.Label
		if label == "" {
			label = benchTransportLabel[rs.Transport]
		}
		keys := make([]string, 0, len(rs.PeakKB))
		for k := range rs.PeakKB {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "| %s | %s | %.1f MiB |\n", label, k, float64(rs.PeakKB[k])/1024)
		}
	}

	if len(r.Diagnostics) > 0 {
		fmt.Fprintf(&b, "\n## Diagnostics\n\nRaw tc/wg state captured per WAN cell, so a result can be reconciled against the qdisc's own drop/overlimit counters (and, for WireGuard, wg0's MTU and transfer stats) instead of taken on faith.\n")
		for _, d := range r.Diagnostics {
			fmt.Fprintf(&b, "\n### %s / %s — %s\n\n```\n%s\n```\n", d.Transport, d.Condition, d.Name, strings.TrimRight(d.Output, "\n"))
		}
	}

	fmt.Fprintf(&b, "\n## Reading the numbers\n\n")
	fmt.Fprintln(&b, "- **Pure SSH** is the ceiling: one OpenSSH connection straight across the Compose bridge.")
	fmt.Fprintln(&b, "- **WireGuard** is a direct peer-to-peer link (mesh-style): one hop fewer than tw, encryption in the kernel — its CPU shows up only in the host-wide CPU-seconds, never in a container's cgroup.")
	fmt.Fprintln(&b, "- **Tunnel Whisperer** is the real product path through the relay. For scp that is SSH inside tw's own end-to-end SSH inside TLS; the iperf3 rows have no inner OpenSSH and are tw's own ceiling.")
	fmt.Fprintln(&b, "- LAN cells measure encryption/framing cost only (~0 RTT). WAN cells add netem delay and loss on both ends; that is where TCP-over-TLS-over-TCP and UDP diverge.")
	fmt.Fprintln(&b, "- Gbit/s per busy core = iperf3 1-stream goodput ÷ average host cores busy during the run.")
	return b.String()
}

const (
	benchChartFile     = "bench-chart.svg"
	benchChartDarkFile = "bench-chart-dark.svg"
)

// chartPalette is the categorical + ink palette for the benchmark SVG
// figure. Both the light and dark instances are CVD-validated (dataviz
// palette validator) against their respective surface colours.
type chartPalette struct {
	Surface       string
	TextPrimary   string
	TextSecondary string
	TextMuted     string
	Grid          string
	SSH           string
	WG            string
	TW            string
}

func lightPalette() chartPalette {
	return chartPalette{
		Surface: "#fcfcfb", TextPrimary: "#0b0b0b", TextSecondary: "#52514e", TextMuted: "#8a8985",
		Grid: "#e6e5e1", SSH: "#2a78d6", WG: "#eb6834", TW: "#1baf7a",
	}
}

func darkPalette() chartPalette {
	return chartPalette{
		Surface: "#1a1a19", TextPrimary: "#ffffff", TextSecondary: "#c3c2b7", TextMuted: "#8a8985",
		Grid: "#33332f", SSH: "#3987e5", WG: "#d95926", TW: "#199e70",
	}
}

func (p chartPalette) color(tr string) string {
	switch tr {
	case trSSH:
		return p.SSH
	case trWG:
		return p.WG
	case trTW:
		return p.TW
	default:
		return p.TextMuted
	}
}

var benchTransportShort = map[string]string{trSSH: "SSH", trWG: "WireGuard", trTW: "tw"}

// wanLabelRe pulls "30 ms" / "0.1" out of a WAN benchConditionInfo.Label
// like "WAN (30 ms RTT, 0.1 % loss)" for the WAN panel's subtitle.
var wanLabelRe = regexp.MustCompile(`\((\d+(?:\.\d+)?)\s*ms[^,]*,\s*([\d.]+)\s*%`)

// xmlEsc escapes the three characters that matter inside SVG text content
// and attribute values built from free-form Env strings.
func xmlEsc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	return strings.ReplaceAll(s, ">", "&gt;")
}

// approxTextWidth estimates a rendered text extent. It is only used to lay
// out the right-aligned legend group (three items whose combined width
// isn't known ahead of render time) — every other label in the figure
// centres or starts itself via SVG's own text-anchor.
func approxTextWidth(s string, fontSize float64) float64 {
	return float64(len([]rune(s))) * fontSize * 0.58
}

const (
	chartW, chartH = 1432, 380 // 6 panels: 24 left margin + 6*216 + 5*16 gutters + 32 right margin
	chartPanelW    = 216
	chartGutter    = 16
	chartPanelX0   = 24
	chartPanelTop  = 56
	chartBaselineY = 320
	chartPlotTop   = 96
	chartBarW      = 20
	chartBarRadius = 4.0
	chartFooterY   = 366
)

// roundedTopRectPath returns a bar path from top to bottom (baseline) at
// [x, x+width] with rounded top corners and a flat bottom — radius 0
// degenerates to a plain rect.
func roundedTopRectPath(x, top, bottom, width, radius float64) string {
	h := bottom - top
	if radius > h {
		radius = h
	}
	if radius < 0 {
		radius = 0
	}
	right := x + width
	if radius == 0 {
		// A plain rect, deliberately with no curve command in the path —
		// this lets tests distinguish a rounded (topmost) segment from a
		// square one by checking for "Q" in the emitted d attribute.
		return fmt.Sprintf("M%.2f,%.2f L%.2f,%.2f L%.2f,%.2f L%.2f,%.2f Z",
			x, bottom, x, top, right, top, right, bottom)
	}
	return fmt.Sprintf("M%.2f,%.2f L%.2f,%.2f Q%.2f,%.2f %.2f,%.2f L%.2f,%.2f Q%.2f,%.2f %.2f,%.2f L%.2f,%.2f Z",
		x, bottom,
		x, top+radius,
		x, top, x+radius, top,
		right-radius, top,
		right, top, right, top+radius,
		right, bottom,
	)
}

// renderPanelHeader draws a panel's title + one or two subtitle lines.
func renderPanelHeader(b *strings.Builder, p chartPalette, px float64, title string, subtitle []string) {
	fmt.Fprintf(b, `<text x="%.2f" y="%d" font-size="12" font-weight="600" fill="%s">%s</text>`,
		px, chartPanelTop+12, p.TextPrimary, xmlEsc(title))
	for i, line := range subtitle {
		fmt.Fprintf(b, `<text x="%.2f" y="%d" font-size="11" fill="%s">%s</text>`,
			px, chartPanelTop+26+i*12, p.TextSecondary, xmlEsc(line))
	}
}

// renderPanelAxis draws the two gridlines (skipped when there is no data at
// all) and the baseline for a panel.
func renderPanelAxis(b *strings.Builder, p chartPalette, px, plotH, maxV float64) {
	if maxV > 0 {
		for _, frac := range []float64{0.5, 1.0} {
			y := float64(chartBaselineY) - frac*0.88*plotH
			fmt.Fprintf(b, `<line x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f" stroke="%s" stroke-width="1"/>`,
				px, y, px+chartPanelW, y, p.Grid)
		}
	}
	fmt.Fprintf(b, `<line x1="%.2f" y1="%d" x2="%.2f" y2="%d" stroke="%s" stroke-width="1"/>`,
		px, chartBaselineY, px+chartPanelW, chartBaselineY, p.TextMuted)
}

// renderBarPanel draws one of the four simple (non-stacked) bar panels:
// one bar per transport, min–max whisker, value label above, transport
// label under the baseline.
func renderBarPanel(b *strings.Builder, p chartPalette, idx int, title string, subtitle []string, labelFmt string, stat func(tr string) benchStat) {
	px := float64(chartPanelX0 + idx*(chartPanelW+chartGutter))
	renderPanelHeader(b, p, px, title, subtitle)

	stats := make([]benchStat, len(benchTransportOrder))
	maxV := 0.0
	for i, tr := range benchTransportOrder {
		stats[i] = stat(tr)
		if stats[i].N > 0 && stats[i].Max > maxV {
			maxV = stats[i].Max
		}
	}

	plotH := float64(chartBaselineY - chartPlotTop)
	renderPanelAxis(b, p, px, plotH, maxV)

	slotW := float64(chartPanelW) / 3
	for i, tr := range benchTransportOrder {
		cx := px + slotW*float64(i) + slotW/2
		color := p.color(tr)
		st := stats[i]
		if st.N == 0 {
			fmt.Fprintf(b, `<text x="%.2f" y="%d" font-size="11" text-anchor="middle" fill="%s">—</text>`,
				cx, chartBaselineY-10, p.TextPrimary)
		} else {
			h := 0.0
			if maxV > 0 {
				h = st.Med / maxV * 0.88 * plotH
			}
			top := float64(chartBaselineY) - h
			d := roundedTopRectPath(cx-chartBarW/2, top, float64(chartBaselineY), chartBarW, chartBarRadius)
			fmt.Fprintf(b, `<path d="%s" fill="%s"/>`, d, color)
			if st.Max > st.Min {
				minY := float64(chartBaselineY) - st.Min/maxV*0.88*plotH
				maxY := float64(chartBaselineY) - st.Max/maxV*0.88*plotH
				fmt.Fprintf(b, `<line class="whisker" x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f" stroke="%s" stroke-opacity="0.6" stroke-width="1.5"/>`,
					cx, minY, cx, maxY, color)
				fmt.Fprintf(b, `<line class="whisker" x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f" stroke="%s" stroke-opacity="0.6" stroke-width="1.5"/>`,
					cx-3, minY, cx+3, minY, color)
				fmt.Fprintf(b, `<line class="whisker" x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f" stroke="%s" stroke-opacity="0.6" stroke-width="1.5"/>`,
					cx-3, maxY, cx+3, maxY, color)
			}
			fmt.Fprintf(b, `<text x="%.2f" y="%.2f" font-size="11" text-anchor="middle" fill="%s">%s</text>`,
				cx, top-6, p.TextPrimary, fmt.Sprintf(labelFmt, st.Med))
		}
		fmt.Fprintf(b, `<text x="%.2f" y="%d" font-size="11" text-anchor="middle" fill="%s">%s</text>`,
			cx, chartBaselineY+16, p.TextSecondary, benchTransportShort[tr])
	}
}

// renderStackPanel draws the fifth panel: a stacked client/server/relay
// CPU-seconds bar per transport, with a 2 px surface-coloured gap between
// segments and a total label above the stack. No whiskers.
func renderStackPanel(b *strings.Builder, p chartPalette, idx int, r *benchResults) {
	px := float64(chartPanelX0 + idx*(chartPanelW+chartGutter))
	renderPanelHeader(b, p, px, "CPU per 5 GiB copy", []string{
		"LAN · CPU-s per scp run",
		"stack: client → server → relay",
	})

	type totals struct {
		values [3]float64 // client, server, relay medians
		total  float64
		has    bool
	}
	tt := make([]totals, len(benchTransportOrder))
	maxV := 0.0
	for i, tr := range benchTransportOrder {
		cl := summarize(r.values(tr, condLAN, instScp, func(s benchSample) float64 { return s.ContainerCPUSec["client"] }))
		sv := summarize(r.values(tr, condLAN, instScp, func(s benchSample) float64 { return s.ContainerCPUSec["server"] }))
		rl := summarize(r.values(tr, condLAN, instScp, func(s benchSample) float64 { return s.ContainerCPUSec["relay"] }))
		t := totals{values: [3]float64{cl.Med, sv.Med, rl.Med}, has: cl.N > 0}
		if t.has {
			t.total = cl.Med + sv.Med + rl.Med
			if t.total > maxV {
				maxV = t.total
			}
		}
		tt[i] = t
	}

	plotH := float64(chartBaselineY - chartPlotTop)
	renderPanelAxis(b, p, px, plotH, maxV)

	opacities := [3]float64{1.0, 0.7, 0.45}
	slotW := float64(chartPanelW) / 3
	for i, tr := range benchTransportOrder {
		cx := px + slotW*float64(i) + slotW/2
		color := p.color(tr)
		t := tt[i]
		if !t.has {
			fmt.Fprintf(b, `<text x="%.2f" y="%d" font-size="11" text-anchor="middle" fill="%s">—</text>`,
				cx, chartBaselineY-10, p.TextPrimary)
		} else {
			// Decide visibility on the *rendered* pixel height, not the raw
			// value: cgroup CPU-accounting noise (e.g. relay ≈0.004s for a
			// transport with no relay hop) can be > 0 but round to a
			// sub-pixel sliver. An invisible segment would still consume a
			// <path> and — worse — steal the rounded-top treatment from the
			// real topmost segment, leaving it visibly square.
			heights := [3]float64{}
			if maxV > 0 {
				for vi, v := range t.values {
					heights[vi] = v / maxV * 0.88 * plotH
				}
			}
			var visible []int
			for vi, h := range heights {
				if h >= 0.5 {
					visible = append(visible, vi)
				}
			}
			segClass := [3]string{"seg-client", "seg-server", "seg-relay"}
			y := float64(chartBaselineY)
			for k, vi := range visible {
				top := y - heights[vi]
				radius := 0.0
				if k == len(visible)-1 {
					radius = chartBarRadius
				}
				d := roundedTopRectPath(cx-chartBarW/2, top, y, chartBarW, radius)
				fmt.Fprintf(b, `<path class="seg %s" d="%s" fill="%s" fill-opacity="%.2f"/>`, segClass[vi], d, color, opacities[vi])
				y = top
				if k != len(visible)-1 {
					y -= 2
				}
			}
			fmt.Fprintf(b, `<text x="%.2f" y="%.2f" font-size="11" text-anchor="middle" fill="%s">%.0f</text>`,
				cx, y-6, p.TextPrimary, t.total)
		}
		fmt.Fprintf(b, `<text x="%.2f" y="%d" font-size="11" text-anchor="middle" fill="%s">%s</text>`,
			cx, chartBaselineY+16, p.TextSecondary, benchTransportShort[tr])
	}
}

// renderBenchSVG draws the five-panel figure. dark selects the dark-surface
// palette. Every number drawn comes from r — nothing is hard-coded.
func renderBenchSVG(r *benchResults, dark bool) string {
	p := lightPalette()
	if dark {
		p = darkPalette()
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" role="img" font-family="ui-sans-serif, system-ui, -apple-system, Segoe UI, Inter, sans-serif">`,
		chartW, chartH, chartW, chartH)
	b.WriteString(`<title>Tunnel Whisperer benchmark — pure SSH vs WireGuard vs Tunnel Whisperer</title>`)
	fmt.Fprintf(&b, `<rect x="0" y="0" width="%d" height="%d" rx="8" fill="%s"/>`, chartW, chartH, p.Surface)

	fmt.Fprintf(&b, `<text x="%d" y="26" font-size="15" font-weight="600" fill="%s">Tunnel Whisperer benchmark — medians of 3 runs, min–max whiskers</text>`,
		chartPanelX0, p.TextPrimary)

	legend := []struct{ tr, label string }{
		{trSSH, benchTransportLabel[trSSH]},
		{trWG, benchTransportLabel[trWG]},
		{trTW, benchTransportLabel[trTW]},
	}
	const legendGap, swatchSize, swatchTextGap = 18.0, 10.0, 6.0
	widths := make([]float64, len(legend))
	total := 0.0
	for i, e := range legend {
		widths[i] = swatchSize + swatchTextGap + approxTextWidth(e.label, 12)
		total += widths[i]
	}
	total += legendGap * float64(len(legend)-1)
	lx := float64(chartW-chartPanelX0) - total
	for i, e := range legend {
		fmt.Fprintf(&b, `<rect x="%.2f" y="17" width="%.0f" height="%.0f" rx="2" fill="%s"/>`,
			lx, swatchSize, swatchSize, p.color(e.tr))
		fmt.Fprintf(&b, `<text x="%.2f" y="25" font-size="12" fill="%s">%s</text>`,
			lx+swatchSize+swatchTextGap, p.TextSecondary, xmlEsc(e.label))
		lx += widths[i] + legendGap
	}

	var lanCond, wanCond *benchConditionInfo
	for i := range r.Conditions {
		switch r.Conditions[i].Name {
		case condLAN:
			lanCond = &r.Conditions[i]
		case condWAN:
			wanCond = &r.Conditions[i]
		}
	}
	scpLabel := ""
	if lanCond != nil {
		scpLabel = fmtScpPayload(lanCond.ScpPayloadBytes)
	}
	wanDerived := ""
	if wanCond != nil {
		if m := wanLabelRe.FindStringSubmatch(wanCond.Label); m != nil {
			wanDerived = fmt.Sprintf(" %s ms / %s %%", m[1], m[2])
		}
	}

	renderBarPanel(&b, p, 0, "LAN throughput", []string{"LAN · iperf3, 1 stream · Gbit/s"}, "%.2f",
		func(tr string) benchStat { return summarize(r.values(tr, condLAN, instIperf1, value)) })
	renderBarPanel(&b, p, 1, "LAN file copy", []string{"LAN · scp " + scpLabel + " · MB/s"}, "%.0f",
		func(tr string) benchStat { return summarize(r.values(tr, condLAN, instScp, value)) })
	renderBarPanel(&b, p, 2, "LAN latency", []string{"LAN · TCP_RR p50 · µs", "(lower is better)"}, "%.0f",
		func(tr string) benchStat {
			return summarize(r.values(tr, condLAN, instRR, func(s benchSample) float64 { return s.P50Us }))
		})
	renderBarPanel(&b, p, 3, "WAN latency", []string{"WAN" + wanDerived + " · TCP_RR p50 · ms", "(lower is better)"}, "%.1f",
		func(tr string) benchStat {
			return summarize(r.values(tr, condWAN, instRR, func(s benchSample) float64 { return s.P50Us / 1000 }))
		})
	renderBarPanel(&b, p, 4, "Lossy WAN, parallel streams", []string{"WAN" + wanDerived, "iperf3, 4 streams · Gbit/s"}, "%.2f",
		func(tr string) benchStat { return summarize(r.values(tr, condWAN, instIperf4, value)) })
	renderStackPanel(&b, p, 5, r)

	cpu := strings.ReplaceAll(strings.ReplaceAll(r.Env.CPUModel, "(R)", ""), "(TM)", "")
	cpu = strings.Join(strings.Fields(cpu), " ")
	if rs := []rune(cpu); len(rs) > 40 {
		cpu = string(rs[:40]) + "…"
	}
	footer := fmt.Sprintf("Generated by make bench from e2e/bench-results.json · %s · %s · offloads/gso_max_segs off on all containers (LAN ceilings are software-segmentation ceilings)",
		r.Env.Generated, cpu)
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="10.5" fill="%s">%s</text>`,
		chartPanelX0, chartFooterY, p.TextMuted, xmlEsc(footer))

	b.WriteString(`</svg>`)
	return b.String()
}

// writeBenchResults writes the JSON + Markdown reports, and the light/dark
// SVG charts, into the working directory (e2e/) and echoes the Markdown,
// pass or fail.
func writeBenchResults(t *testing.T, r *benchResults) {
	t.Helper()
	js, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Errorf("marshal bench results: %v", err)
	} else if err := os.WriteFile(benchResultsFile, js, 0o644); err != nil {
		t.Errorf("write %s: %v", benchResultsFile, err)
	}
	md := renderBenchMarkdown(r)
	if err := os.WriteFile(benchReportFile, []byte(md), 0o644); err != nil {
		t.Errorf("write %s: %v", benchReportFile, err)
	}
	if err := os.WriteFile(benchChartFile, []byte(renderBenchSVG(r, false)), 0o644); err != nil {
		t.Errorf("write %s: %v", benchChartFile, err)
	}
	if err := os.WriteFile(benchChartDarkFile, []byte(renderBenchSVG(r, true)), 0o644); err != nil {
		t.Errorf("write %s: %v", benchChartDarkFile, err)
	}
	fmt.Fprintf(os.Stdout, "\n%s\n full report: e2e/%s (raw: e2e/%s)\n charts: e2e/%s, e2e/%s\n",
		md, benchReportFile, benchResultsFile, benchChartFile, benchChartDarkFile)
}
