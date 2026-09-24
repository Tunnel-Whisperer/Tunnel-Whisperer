package ops

import (
	"fmt"
	"log/slog"
	"os"

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

// resolveServerID returns the stored server id, else the CN of the client
// cert at clientCertPath when it is a valid server id (and not the legacy
// relay-host CN), else the hostname + UUID derivation. It never persists.
func resolveServerID(cfg *config.Config, clientCertPath string) string {
	if cfg.Xray.ServerID != "" {
		return cfg.Xray.ServerID
	}
	if cn := certCN(clientCertPath); cn != "" && joinServerIDRe.MatchString(cn) && cn != cfg.Xray.RelayHost {
		return cn
	}
	hostname, _ := os.Hostname()
	return deriveServerID(hostname, cfg.Xray.UUID)
}

// serverID returns this profile's tenant identity, resolving and persisting
// it on first use so a later hostname change cannot alter it. Client profiles
// have no tenant identity and get "".
func (o *Ops) serverID() (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cfg.Mode == "client" {
		return "", nil
	}
	if o.cfg.Xray.ServerID == "" {
		id := resolveServerID(o.cfg, config.ClientCertPath())
		o.cfg.Xray.ServerID = id
		if err := config.Save(o.cfg); err != nil {
			o.cfg.Xray.ServerID = ""
			return "", fmt.Errorf("persisting server id: %w", err)
		}
		slog.Info("server id persisted", "id", id)
	}
	return o.cfg.Xray.ServerID, nil
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
