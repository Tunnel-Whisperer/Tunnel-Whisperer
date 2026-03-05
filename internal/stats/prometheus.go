package stats

import (
	"fmt"
	"io"
	"strings"
)

// WritePrometheus writes all metrics in Prometheus text exposition format.
func (c *Collector) WritePrometheus(w io.Writer) {
	snaps := c.Snapshot()

	sections := []struct {
		name, help, typ string
		val             func(Snapshot) int64
	}{
		{"tw_tunnel_bytes_sent_total", "Total bytes sent (upload) per tunnel.", "counter", func(s Snapshot) int64 { return s.BytesSent }},
		{"tw_tunnel_bytes_received_total", "Total bytes received (download) per tunnel.", "counter", func(s Snapshot) int64 { return s.BytesRecv }},
		{"tw_tunnel_connections_active", "Currently active forwarded connections per tunnel.", "gauge", func(s Snapshot) int64 { return s.ActiveConn }},
		{"tw_tunnel_connections_total", "Total connections established per tunnel.", "counter", func(s Snapshot) int64 { return s.TotalConn }},
	}

	for _, sec := range sections {
		fmt.Fprintf(w, "# HELP %s %s\n", sec.name, sec.help)
		fmt.Fprintf(w, "# TYPE %s %s\n", sec.name, sec.typ)
		for _, s := range snaps {
			labels := fmt.Sprintf(`user="%s",port="%d"`, promEscape(s.User), s.Port)
			fmt.Fprintf(w, "%s{%s} %d\n", sec.name, labels, sec.val(s))
		}
	}
}

// promEscape escapes label values for Prometheus text format.
func promEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}
