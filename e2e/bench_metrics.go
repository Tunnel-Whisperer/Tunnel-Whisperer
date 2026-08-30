//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// cpuTimes is one /proc/stat aggregate-cpu snapshot in USER_HZ jiffies
// (100 Hz on Linux). busy = user+nice+system+irq+softirq+steal — everything
// that is not idle/iowait. Read on the Docker host, this is the only counter
// that includes kernel WireGuard's work (its crypto runs in wg-crypt/wg-kex
// kthreads and softirq context, outside every container cgroup).
type cpuTimes struct{ busy, total uint64 }

// busySecondsSince converts the busy-jiffy delta since prev to seconds.
func (c cpuTimes) busySecondsSince(prev cpuTimes) float64 {
	return float64(c.busy-prev.busy) / 100
}

func parseProcStat(s string) (cpuTimes, error) {
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 9 || f[0] != "cpu" {
			continue
		}
		var v [8]uint64 // user nice system idle iowait irq softirq steal
		for i := range v {
			n, err := strconv.ParseUint(f[i+1], 10, 64)
			if err != nil {
				return cpuTimes{}, fmt.Errorf("parse /proc/stat field %d %q: %w", i, f[i+1], err)
			}
			v[i] = n
		}
		busy := v[0] + v[1] + v[2] + v[5] + v[6] + v[7]
		return cpuTimes{busy: busy, total: busy + v[3] + v[4]}, nil
	}
	return cpuTimes{}, fmt.Errorf("no aggregate cpu line in /proc/stat")
}

// hostCPU reads the Docker host's /proc/stat (the test runs on the host).
func hostCPU(t *testing.T) cpuTimes {
	t.Helper()
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		t.Fatalf("read host /proc/stat: %v", err)
	}
	c, err := parseProcStat(string(b))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func parseCgroupUsageUsec(s string) (uint64, error) {
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "usage_usec" {
			v, err := strconv.ParseUint(f[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse cpu.stat usage_usec %q: %w", f[1], err)
			}
			return v, nil
		}
	}
	return 0, fmt.Errorf("no usage_usec line in cpu.stat")
}

// containerCPUUsec returns the container's cgroup v2 CPU usage. Inside the
// container /sys/fs/cgroup is its own cgroup (private cgroupns), so this is
// exactly the CPU consumed by that container's processes.
func containerCPUUsec(t *testing.T, service string) uint64 {
	t.Helper()
	out := execIn(t, service, "cat /sys/fs/cgroup/cpu.stat")
	v, err := parseCgroupUsageUsec(out)
	if err != nil {
		fatalf(t, "%s cpu.stat: %v\n%s", service, err, out)
	}
	return v
}

// ifaceRxBytes returns eth0's cumulative received bytes in the container —
// the bytes that actually crossed the Compose network into it, whatever the
// encapsulation.
func ifaceRxBytes(t *testing.T, service string) uint64 {
	t.Helper()
	out := strings.TrimSpace(execIn(t, service, "cat /sys/class/net/eth0/statistics/rx_bytes"))
	v, err := strconv.ParseUint(out, 10, 64)
	if err != nil {
		fatalf(t, "%s eth0 rx_bytes %q: %v", service, out, err)
	}
	return v
}

func parseVmHWMKB(status string) (uint64, error) {
	for _, line := range strings.Split(status, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "VmHWM:" {
			v, err := strconv.ParseUint(f[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse VmHWM %q: %w", f[1], err)
			}
			return v, nil
		}
	}
	return 0, fmt.Errorf("no VmHWM line in /proc/<pid>/status")
}

// peakRSSKB returns the largest VmHWM (peak resident set size, kB) among
// the processes in service whose /proc/<pid>/cmdline contains substr, or 0
// when none match. Walks /proc like killMatching (the tw image has no
// procps) and skips the walking shell's own PID, whose cmdline contains the
// search text.
func peakRSSKB(t *testing.T, service, substr string) uint64 {
	t.Helper()
	script := `max=0; for p in /proc/[0-9]*; do ` +
		`pid=${p#/proc/}; [ "$pid" = "$$" ] && continue; ` +
		`cmd=$(tr '\0' ' ' < "$p/cmdline" 2>/dev/null) || continue; ` +
		`case "$cmd" in *"` + substr + `"*) ` +
		`kb=$(awk '/^VmHWM:/{print $2}' "$p/status" 2>/dev/null); ` +
		`[ -n "$kb" ] && [ "$kb" -gt "$max" ] && max=$kb;; esac; ` +
		`done; echo $max`
	out := strings.TrimSpace(execIn(t, service, script))
	v, err := strconv.ParseUint(out, 10, 64)
	if err != nil {
		fatalf(t, "peakRSSKB(%s, %q): unexpected output %q: %v", service, substr, out, err)
	}
	return v
}

// parseIperfJSON extracts end.sum_received.bits_per_second from `iperf3 -J`
// output. sum_received is the receiver-side goodput and exists for both
// single- and multi-stream runs.
func parseIperfJSON(b []byte) (float64, error) {
	var v struct {
		Error string `json:"error"`
		End   struct {
			SumReceived struct {
				BitsPerSecond float64 `json:"bits_per_second"`
			} `json:"sum_received"`
		} `json:"end"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return 0, fmt.Errorf("iperf3 json: %w", err)
	}
	if v.Error != "" {
		return 0, fmt.Errorf("iperf3: %s", v.Error)
	}
	if v.End.SumReceived.BitsPerSecond == 0 {
		return 0, fmt.Errorf("iperf3 json: missing end.sum_received.bits_per_second")
	}
	return v.End.SumReceived.BitsPerSecond, nil
}

type netperfResult struct {
	TransPerSec, MeanUs, P50Us, P99Us float64
}

// parseNetperfCSV parses the two-line (header, values) CSV that
// `netperf ... -o THROUGHPUT,MEAN_LATENCY,P50_LATENCY,P99_LATENCY` prints.
// Only the last two non-empty lines are used, so a leading banner line is
// harmless.
func parseNetperfCSV(s string) (netperfResult, error) {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	if len(lines) < 2 {
		return netperfResult{}, fmt.Errorf("netperf output has fewer than two lines:\n%s", s)
	}
	hdr := strings.Split(lines[len(lines)-2], ",")
	vals := strings.Split(lines[len(lines)-1], ",")
	if len(hdr) != len(vals) {
		return netperfResult{}, fmt.Errorf("netperf header has %d columns, values %d:\n%s", len(hdr), len(vals), s)
	}
	col := func(name string) (float64, error) {
		for i, h := range hdr {
			if strings.TrimSpace(h) == name {
				v, err := strconv.ParseFloat(strings.TrimSpace(vals[i]), 64)
				if err != nil {
					return 0, fmt.Errorf("parse netperf column %q value %q: %w", name, vals[i], err)
				}
				return v, nil
			}
		}
		return 0, fmt.Errorf("netperf output has no column %q", name)
	}
	var r netperfResult
	var err error
	if r.TransPerSec, err = col("Throughput"); err != nil {
		return r, err
	}
	if r.MeanUs, err = col("Mean Latency Microseconds"); err != nil {
		return r, err
	}
	if r.P50Us, err = col("50th Percentile Latency Microseconds"); err != nil {
		return r, err
	}
	if r.P99Us, err = col("99th Percentile Latency Microseconds"); err != nil {
		return r, err
	}
	return r, nil
}

// parseNetemDropRatio extracts the drop count and packet count from a
// netem qdisc's stats line in `tc -s qdisc show dev eth0` output, e.g.
// " Sent 297103728 bytes 2072112 pkt (dropped 338, overlimits 0 requeues 0)".
// tc's "pkt" count is the number of skbs handed to the qdisc, which is the
// same unit "dropped" is counted in, so dropped/(sent+dropped) is the true
// drop ratio regardless of how many wire packets each skb becomes.
func parseNetemDropRatio(s string) (dropped, sent uint64, ratioPct float64, err error) {
	m := regexp.MustCompile(`Sent \d+ bytes (\d+) pkt \(dropped (\d+), overlimits \d+ requeues \d+\)`).FindStringSubmatch(s)
	if m == nil {
		return 0, 0, 0, fmt.Errorf("no netem Sent/dropped stats line found")
	}
	sent, err = strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parse sent pkt %q: %w", m[1], err)
	}
	dropped, err = strconv.ParseUint(m[2], 10, 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parse dropped %q: %w", m[2], err)
	}
	if sent+dropped == 0 {
		return dropped, sent, 0, nil
	}
	return dropped, sent, float64(dropped) / float64(sent+dropped) * 100, nil
}
