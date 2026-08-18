package ops

import (
	"fmt"

	"github.com/tunnelwhisperer/tw/internal/config"
)

// portRange caps tenants per relay; bump if ever needed.
const portRange = 1000

func sanitizeHostname(s string) string {
	if out := config.SanitizeName(s); out != "" {
		return out
	}
	return "tw"
}

func first8(uuid string) string {
	return config.ShortID(uuid)
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

// enrollPort is where an issuer's enrollment listener binds on the relay
// loopback: its tunnel port + 20000. Derived, never stored — every full
// render self-heals it for all tenants. Tenant tunnel ports allocate from
// 20000 up and Caddy upstreams sit at tunnel+10000, so the three ranges stay
// disjoint for any realistic tenant count (<2000).
func enrollPort(remotePort int) int { return remotePort + 20000 }
