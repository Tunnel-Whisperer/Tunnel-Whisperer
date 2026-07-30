//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"text/tabwriter"
	"time"
)

// reportFile is written next to the test binary's working directory (e2e/)
// at the end of every TestE2E run, pass or fail.
const reportFile = "e2e-report.md"

// scenarioResult is one row of the final report. Scenarios run strictly
// sequentially (dependency order), so no locking is needed.
type scenarioResult struct {
	name     string
	summary  string // filled by scenario()
	checks   []string
	status   string // "PASS", "FAIL", "SKIP", "NOT RUN"
	detail   string // skip reason, or why the scenario never ran
	duration time.Duration
}

var (
	results []*scenarioResult
	current *scenarioResult // scenario being executed; annotated by scenario()/skipScenario()
)

// initReport registers every scenario up front as NOT RUN, so scenarios never
// reached (fail-fast stop, or filtered out by -run) still appear in the table.
func initReport(names []string) {
	results = make([]*scenarioResult, len(names))
	for i, n := range names {
		results[i] = &scenarioResult{
			name:   n,
			status: "NOT RUN",
			detail: "not reached — earlier scenario failed, or filtered out by -run",
		}
	}
}

// runScenario wraps t.Run so the subtest's outcome and duration land in the
// report regardless of how it exits (pass, Fatalf, or Skip).
func runScenario(t *testing.T, name string, fn func(*testing.T)) bool {
	var res *scenarioResult
	for _, r := range results {
		if r.name == name {
			res = r
			break
		}
	}
	return t.Run(name, func(st *testing.T) {
		current = res
		start := time.Now()
		defer func() {
			current = nil
			res.duration = time.Since(start)
			switch {
			case st.Skipped():
				res.status = "SKIP"
			case st.Failed():
				res.status = "FAIL"
			default:
				res.status = "PASS"
				res.detail = ""
			}
		}()
		fn(st)
	})
}

// skipScenario records the skip reason in the report, then skips the subtest.
func skipScenario(t *testing.T, reason string) {
	t.Helper()
	if current != nil {
		current.detail = reason
	}
	t.Skip(reason)
}

// writeReport prints the scenario table to stdout and writes the full
// markdown report to reportFile. Called via defer from TestE2E so it runs
// even when a scenario failure stops the suite.
func writeReport(t *testing.T) {
	counts := map[string]int{}
	for _, r := range results {
		counts[r.status]++
	}
	totals := fmt.Sprintf("%d passed, %d failed, %d skipped, %d not run",
		counts["PASS"], counts["FAIL"], counts["SKIP"], counts["NOT RUN"])

	// Stdout table — bypasses the testing log so it prints untruncated and
	// unprefixed at the end of the run.
	var b strings.Builder
	line := strings.Repeat("=", 100)
	fmt.Fprintf(&b, "\n%s\n E2E SCENARIO REPORT — %s\n%s\n", line, totals, line)
	w := tabwriter.NewWriter(&b, 2, 0, 3, ' ', 0)
	fmt.Fprintln(w, " #\tSCENARIO\tRESULT\tDURATION\tWHAT IT VERIFIES")
	for i, r := range results {
		fmt.Fprintf(w, " %d\t%s\t%s\t%s\t%s\n", i+1, r.name, r.status, fmtDuration(r), oneLine(r))
	}
	w.Flush()
	fmt.Fprintf(&b, "%s\n full report: e2e/%s\n%s\n", line, reportFile, line)
	fmt.Fprint(os.Stdout, b.String())

	if err := os.WriteFile(reportFile, []byte(markdownReport(totals)), 0o644); err != nil {
		t.Logf("could not write %s: %v", reportFile, err)
	}
}

func markdownReport(totals string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Tunnel Whisperer E2E Scenario Report\n\n")
	fmt.Fprintf(&b, "Generated: %s\n\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "Data path under test: client → Xray VLESS/XHTTP/mTLS :443 → Caddy client_auth gate → relay Xray → reverse SSH → server\n\n")
	fmt.Fprintf(&b, "**Totals:** %s\n\n", totals)
	fmt.Fprintln(&b, "| # | Scenario | Result | Duration | What it verifies |")
	fmt.Fprintln(&b, "|---|----------|--------|----------|------------------|")
	for i, r := range results {
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s |\n",
			i+1, r.name, r.status, fmtDuration(r), strings.ReplaceAll(oneLine(r), "|", "\\|"))
	}
	fmt.Fprintf(&b, "\n## Scenario details\n")
	for i, r := range results {
		fmt.Fprintf(&b, "\n### %d. %s — %s (%s)\n\n%s\n", i+1, r.name, r.status, fmtDuration(r), oneLine(r))
		for _, c := range r.checks {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	return b.String()
}

// oneLine is the human-readable description column: the scenario() summary
// when the scenario ran, otherwise the skip/not-run reason.
func oneLine(r *scenarioResult) string {
	if r.summary != "" {
		return r.summary
	}
	return r.detail
}

func fmtDuration(r *scenarioResult) string {
	if r.status == "NOT RUN" {
		return "—"
	}
	return r.duration.Round(100 * time.Millisecond).String()
}
