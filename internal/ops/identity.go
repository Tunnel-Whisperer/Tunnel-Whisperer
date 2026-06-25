package ops

import (
	"fmt"
	"strings"
)

// portRange caps tenants per relay; bump if ever needed.
const portRange = 1000

func sanitizeHostname(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "tw"
	}
	return out
}

func first8(uuid string) string {
	u := strings.ReplaceAll(uuid, "-", "")
	if len(u) > 8 {
		return u[:8]
	}
	return u
}

// deriveServerID is the canonical tenant identity: <sanitized-hostname>-<first8-uuid>.
func deriveServerID(hostname, uuid string) string {
	return sanitizeHostname(hostname) + "-" + first8(uuid)
}

// firstFreeFromBase returns the lowest port >= base in [base, base+portRange)
// not present in used. Guarantees no conflict; reclaims freed ports.
func firstFreeFromBase(base int, used []int) (int, error) {
	taken := make(map[int]bool, len(used))
	for _, p := range used {
		taken[p] = true
	}
	for p := base; p < base+portRange; p++ {
		if !taken[p] {
			return p, nil
		}
	}
	return 0, fmt.Errorf("relay tenant capacity (%d) exhausted", portRange)
}
