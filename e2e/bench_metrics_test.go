//go:build e2e

package e2e

import (
	"math"
	"strings"
	"testing"
)

func TestParseProcStat(t *testing.T) {
	// user nice system idle iowait irq softirq steal guest guest_nice
	in := "cpu  100 5 50 1000 20 3 7 1 0 0\ncpu0 1 2 3 4 5 6 7 8 0 0\n"
	got, err := parseProcStat(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.busy != 100+5+50+3+7+1 {
		t.Fatalf("busy = %d, want 166", got.busy)
	}
	if got.total != 166+1000+20 {
		t.Fatalf("total = %d, want 1186", got.total)
	}
	if s := (cpuTimes{busy: 366}).busySecondsSince(got); math.Abs(s-2.0) > 1e-9 {
		t.Fatalf("busySecondsSince = %v, want 2.0 (200 jiffies at 100 Hz)", s)
	}
	if _, err := parseProcStat("intr 1 2 3\n"); err == nil {
		t.Fatal("expected error for input without a cpu line")
	}
}

func TestParseCgroupUsageUsec(t *testing.T) {
	in := "usage_usec 123456\nuser_usec 100000\nsystem_usec 23456\n"
	got, err := parseCgroupUsageUsec(in)
	if err != nil || got != 123456 {
		t.Fatalf("got %d, %v; want 123456", got, err)
	}
	if _, err := parseCgroupUsageUsec("nr_periods 0\n"); err == nil {
		t.Fatal("expected error without usage_usec")
	}
	// Check error wrapping: non-numeric value should include "usage_usec" in error message
	if _, err := parseCgroupUsageUsec("usage_usec abc\n"); err == nil || !strings.Contains(err.Error(), "usage_usec") {
		t.Fatalf("expected error with 'usage_usec' context, got: %v", err)
	}
}

func TestParseVmHWMKB(t *testing.T) {
	in := "Name:\tsshd\nVmPeak:\t   12345 kB\nVmHWM:\t    6789 kB\nVmRSS:\t    6000 kB\n"
	got, err := parseVmHWMKB(in)
	if err != nil || got != 6789 {
		t.Fatalf("got %d, %v; want 6789", got, err)
	}
	if _, err := parseVmHWMKB("Name:\tx\n"); err == nil {
		t.Fatal("expected error without VmHWM")
	}
	// Check error wrapping: non-numeric value should include "VmHWM" in error message
	if _, err := parseVmHWMKB("VmHWM:\t xyz kB\n"); err == nil || !strings.Contains(err.Error(), "VmHWM") {
		t.Fatalf("expected error with 'VmHWM' context, got: %v", err)
	}
}

func TestParseIperfJSON(t *testing.T) {
	ok := []byte(`{"start":{},"intervals":[],"end":{"sum_sent":{"bits_per_second":1.0e9},"sum_received":{"bits_per_second":9.87e8}}}`)
	got, err := parseIperfJSON(ok)
	if err != nil || got != 9.87e8 {
		t.Fatalf("got %v, %v; want 9.87e8", got, err)
	}
	bad := []byte(`{"error":"unable to connect to server: Connection refused"}`)
	if _, err := parseIperfJSON(bad); err == nil {
		t.Fatal("expected iperf3 error to surface")
	}
	if _, err := parseIperfJSON([]byte(`{"end":{}}`)); err == nil {
		t.Fatal("expected error when sum_received is missing")
	}
}

func TestParseNetperfCSV(t *testing.T) {
	in := "Throughput,Mean Latency Microseconds,50th Percentile Latency Microseconds,99th Percentile Latency Microseconds\n" +
		"35854.15,27.84,26,59\n"
	got, err := parseNetperfCSV(in)
	if err != nil {
		t.Fatal(err)
	}
	want := netperfResult{TransPerSec: 35854.15, MeanUs: 27.84, P50Us: 26, P99Us: 59}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	// A banner line before the header (netperf without -P 0) must not break parsing.
	withBanner := "MIGRATED TCP REQUEST/RESPONSE TEST from 0.0.0.0 ...\n" + in
	if got2, err := parseNetperfCSV(withBanner); err != nil || got2 != want {
		t.Fatalf("with banner: got %+v, %v", got2, err)
	}
	if _, err := parseNetperfCSV("Throughput,Mean\n1\n"); err == nil {
		t.Fatal("expected error on header/value count mismatch")
	}
	// Check error wrapping: non-numeric value should include "Throughput" in error message
	badCSV := "Throughput,Mean Latency Microseconds,50th Percentile Latency Microseconds,99th Percentile Latency Microseconds\n" +
		"abc,27.84,26,59\n"
	if _, err := parseNetperfCSV(badCSV); err == nil || !strings.Contains(err.Error(), "Throughput") {
		t.Fatalf("expected error with 'Throughput' context, got: %v", err)
	}
}

func TestParseNetemDropRatio(t *testing.T) {
	in := "qdisc netem 8004: root refcnt 2 limit 100000 delay 15.0ms loss 0.1%\n" +
		" Sent 297103728 bytes 2072112 pkt (dropped 338, overlimits 0 requeues 0) \n" +
		" backlog 0b 0p requeues 0\n"
	dropped, sent, ratio, err := parseNetemDropRatio(in)
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 338 || sent != 2072112 {
		t.Fatalf("dropped=%d sent=%d, want 338/2072112", dropped, sent)
	}
	wantRatio := 338.0 / (2072112.0 + 338.0) * 100
	if math.Abs(ratio-wantRatio) > 1e-9 {
		t.Fatalf("ratio = %v, want %v", ratio, wantRatio)
	}
	if _, _, _, err := parseNetemDropRatio("qdisc noqueue 0: root refcnt 2\n backlog 0b 0p requeues 0\n"); err == nil {
		t.Fatal("expected error on qdisc output with no netem Sent/dropped line")
	}
}
