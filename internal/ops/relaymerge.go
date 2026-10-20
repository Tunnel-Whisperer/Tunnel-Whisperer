package ops

import (
	"encoding/json"
	"fmt"
	"strings"

	gossh "golang.org/x/crypto/ssh"
)

const relayXrayConfigPath = "/usr/local/etc/xray/config.json"

// mergeRelayClients carries client-user UUIDs over from the relay's current
// Xray config into a freshly rendered one. The tenant template emits exactly
// one client per vless-in-<server-id> inbound (the server's own UUID), while
// servers persist their users' UUIDs only by patching that inbound's
// settings.clients on the relay (addUUIDToRelay). Without this, every
// enroll/un-enroll rewrite would strip every server's users (ISS-002).
//
// For each vless-in-* inbound present in both configs, clients from current
// whose id is not already in rendered are appended, in current's order.
// Inbounds only in rendered (new tenant) stay as rendered; inbounds only in
// current (un-enrolled tenant) are dropped; non-vless inbounds are untouched.
func mergeRelayClients(rendered, current map[string]interface{}) {
	currentInbounds, _ := current["inbounds"].([]interface{})
	if len(currentInbounds) == 0 {
		return
	}
	currentByTag := make(map[string]map[string]interface{}, len(currentInbounds))
	for _, ib := range currentInbounds {
		m, ok := ib.(map[string]interface{})
		if !ok {
			continue
		}
		if tag, _ := m["tag"].(string); strings.HasPrefix(tag, "vless-in-") {
			currentByTag[tag] = m
		}
	}

	renderedInbounds, _ := rendered["inbounds"].([]interface{})
	for _, ib := range renderedInbounds {
		m, ok := ib.(map[string]interface{})
		if !ok {
			continue
		}
		tag, _ := m["tag"].(string)
		cur, ok := currentByTag[tag]
		if !ok {
			continue
		}
		settings, _ := m["settings"].(map[string]interface{})
		if settings == nil {
			continue
		}
		clients, _ := settings["clients"].([]interface{})
		present := make(map[string]bool, len(clients))
		for _, c := range clients {
			if cm, ok := c.(map[string]interface{}); ok {
				if id, ok := cm["id"].(string); ok {
					present[id] = true
				}
			}
		}
		curSettings, _ := cur["settings"].(map[string]interface{})
		curClients, _ := curSettings["clients"].([]interface{})
		for _, c := range curClients {
			cm, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			id, ok := cm["id"].(string)
			if !ok || present[id] {
				continue
			}
			clients = append(clients, cm)
			present[id] = true
		}
		settings["clients"] = clients
	}
}

// relayXrayConfigExists reports whether the relay already has a config.json.
// A fresh relay (first enroll) has none; that is the only case where the
// rendered config is written without merging.
func relayXrayConfigExists(client *gossh.Client) (bool, error) {
	session, err := client.NewSession()
	if err != nil {
		return false, fmt.Errorf("opening ssh session: %w", err)
	}
	defer session.Close()
	out, err := session.Output("sudo sh -c 'test -f " + relayXrayConfigPath + " && echo present || echo absent'")
	if err != nil {
		return false, fmt.Errorf("checking for relay config: %w", err)
	}
	switch strings.TrimSpace(string(out)) {
	case "present":
		return true, nil
	case "absent":
		return false, nil
	default:
		return false, fmt.Errorf("checking for relay config: unexpected output %q", string(out))
	}
}

// mergedRelayXrayConfig returns the JSON to persist on the relay for a
// re-rendered tenant list: xjson (from relayxray.RenderConfig) with the
// client-user UUIDs of every surviving inbound carried over from the relay's
// current config.json. If the relay has no config.json yet, xjson is
// returned as rendered; any other read or parse failure is an error.
func mergedRelayXrayConfig(client *gossh.Client, xjson string) ([]byte, error) {
	exists, err := relayXrayConfigExists(client)
	if err != nil {
		return nil, err
	}
	if !exists {
		return []byte(xjson), nil
	}
	current, err := readRelayXrayConfig(client)
	if err != nil {
		return nil, err
	}
	var rendered map[string]interface{}
	if err := json.Unmarshal([]byte(xjson), &rendered); err != nil {
		return nil, fmt.Errorf("parsing rendered relay config: %w", err)
	}
	mergeRelayClients(rendered, current)
	merged, err := json.MarshalIndent(rendered, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling merged relay config: %w", err)
	}
	return merged, nil
}
