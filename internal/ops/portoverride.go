package ops

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tunnelwhisperer/tw/internal/config"
)

// validateClientOverrides checks that every persisted and runtime override
// targets an existing tunnel's server port, that local ports are in range,
// and that the resulting effective local ports are unique.
func validateClientOverrides(c config.ClientConfig, runtime map[int]int) error {
	valid := make(map[int]bool, len(c.Tunnels))
	ports := make([]string, 0, len(c.Tunnels))
	for _, t := range c.Tunnels {
		valid[t.RemotePort] = true
		ports = append(ports, strconv.Itoa(t.RemotePort))
	}
	check := func(overrides map[int]int, source string) error {
		for sp, lp := range overrides {
			if !valid[sp] {
				if len(ports) == 0 {
					return fmt.Errorf("%s: no tunnels configured", source)
				}
				return fmt.Errorf("%s: no tunnel with server port %d (valid server ports: %s)",
					source, sp, strings.Join(ports, ", "))
			}
			if lp < 1 || lp > 65535 {
				return fmt.Errorf("%s: local port %d for server port %d must be between 1 and 65535",
					source, lp, sp)
			}
		}
		return nil
	}
	if err := check(c.PortOverrides, "port_overrides"); err != nil {
		return err
	}
	if err := check(runtime, "--map"); err != nil {
		return err
	}
	seen := map[int]int{}
	for _, t := range c.EffectiveTunnels(runtime) {
		if other, dup := seen[t.LocalPort]; dup {
			return fmt.Errorf("local port %d is used by both server port %d and server port %d",
				t.LocalPort, other, t.RemotePort)
		}
		seen[t.LocalPort] = t.RemotePort
	}
	return nil
}

// SetClientPortOverride records a client-side local-port override for the
// tunnel whose server port is serverPort. Takes effect on next reconnect.
func (o *Ops) SetClientPortOverride(serverPort, localPort int) error {
	o.mu.Lock()
	next := make(map[int]int, len(o.cfg.Client.PortOverrides)+1)
	for k, v := range o.cfg.Client.PortOverrides {
		next[k] = v
	}
	next[serverPort] = localPort
	candidate := o.cfg.Client
	candidate.PortOverrides = next
	if err := validateClientOverrides(candidate, nil); err != nil {
		o.mu.Unlock()
		return err
	}
	o.cfg.Client.PortOverrides = next
	cfg := o.cfg
	o.mu.Unlock()
	return config.Save(cfg)
}

// ClearClientPortOverride removes the override for serverPort. The bool
// reports whether an override existed. Takes effect on next reconnect.
func (o *Ops) ClearClientPortOverride(serverPort int) (bool, error) {
	o.mu.Lock()
	if _, ok := o.cfg.Client.PortOverrides[serverPort]; !ok {
		o.mu.Unlock()
		return false, nil
	}
	var next map[int]int
	if len(o.cfg.Client.PortOverrides) > 1 {
		next = make(map[int]int, len(o.cfg.Client.PortOverrides)-1)
		for k, v := range o.cfg.Client.PortOverrides {
			if k != serverPort {
				next[k] = v
			}
		}
	}
	o.cfg.Client.PortOverrides = next
	cfg := o.cfg
	o.mu.Unlock()
	return true, config.Save(cfg)
}
