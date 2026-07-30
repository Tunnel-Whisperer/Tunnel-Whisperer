package config

import "testing"

func clientCfgForTest() ClientConfig {
	return ClientConfig{
		Tunnels: []Tunnel{
			{LocalPort: 8080, RemoteHost: "127.0.0.1", RemotePort: 15432},
			{LocalPort: 9090, RemoteHost: "127.0.0.1", RemotePort: 15433},
		},
	}
}

func TestEffectiveTunnelsDefaults(t *testing.T) {
	c := clientCfgForTest()
	got := c.EffectiveTunnels(nil)
	if got[0].LocalPort != 8080 || got[1].LocalPort != 9090 {
		t.Errorf("defaults changed: %+v", got)
	}
}

func TestEffectiveTunnelsPersistedOverride(t *testing.T) {
	c := clientCfgForTest()
	c.PortOverrides = map[int]int{15432: 4000}
	got := c.EffectiveTunnels(nil)
	if got[0].LocalPort != 4000 {
		t.Errorf("override not applied: %+v", got[0])
	}
	if got[1].LocalPort != 9090 {
		t.Errorf("unrelated tunnel changed: %+v", got[1])
	}
	if c.Tunnels[0].LocalPort != 8080 {
		t.Errorf("receiver's Tunnels mutated: %+v", c.Tunnels[0])
	}
}

func TestEffectiveTunnelsRuntimeBeatsPersisted(t *testing.T) {
	c := clientCfgForTest()
	c.PortOverrides = map[int]int{15432: 4000}
	got := c.EffectiveTunnels(map[int]int{15432: 5000})
	if got[0].LocalPort != 5000 {
		t.Errorf("runtime override should win: %+v", got[0])
	}
}

func TestEffectiveTunnelsIgnoresUnknownServerPorts(t *testing.T) {
	c := clientCfgForTest()
	c.PortOverrides = map[int]int{99999: 4000}
	got := c.EffectiveTunnels(map[int]int{88888: 5000})
	if got[0].LocalPort != 8080 || got[1].LocalPort != 9090 {
		t.Errorf("unknown keys must be no-ops here (validated elsewhere): %+v", got)
	}
}
