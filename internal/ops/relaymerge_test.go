package ops

import (
	"encoding/json"
	"reflect"
	"testing"
)

// mergeInbound builds one vless-in-<id> inbound as RenderConfig emits it,
// with the given client ids (first one is the server's own UUID).
func mergeInbound(tag string, ids ...string) map[string]interface{} {
	clients := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		clients = append(clients, map[string]interface{}{"id": id, "email": id})
	}
	return map[string]interface{}{
		"tag":      tag,
		"protocol": "vless",
		"settings": map[string]interface{}{"clients": clients, "decryption": "none"},
	}
}

func mergeConf(inbounds ...map[string]interface{}) map[string]interface{} {
	list := make([]interface{}, 0, len(inbounds))
	for _, ib := range inbounds {
		list = append(list, ib)
	}
	return map[string]interface{}{"inbounds": list}
}

func mergeIDs(t *testing.T, conf map[string]interface{}, tag string) []string {
	t.Helper()
	_, clients, err := relayClients(conf, tag)
	if err != nil {
		t.Fatalf("relayClients(%s): %v", tag, err)
	}
	ids := make([]string, 0, len(clients))
	for _, c := range clients {
		ids = append(ids, c.(map[string]interface{})["id"].(string))
	}
	return ids
}

func mergeTags(conf map[string]interface{}) []string {
	var tags []string
	for _, ib := range conf["inbounds"].([]interface{}) {
		tags = append(tags, ib.(map[string]interface{})["tag"].(string))
	}
	return tags
}

func TestMergeRelayClients(t *testing.T) {
	apiInbound := func() map[string]interface{} {
		return map[string]interface{}{"tag": "api", "protocol": "dokodemo-door", "settings": map[string]interface{}{"address": "127.0.0.1"}}
	}

	t.Run("surviving tenants keep their users, new tenant and api untouched", func(t *testing.T) {
		rendered := mergeConf(
			apiInbound(),
			mergeInbound("vless-in-A", "uuid-A"),
			mergeInbound("vless-in-B", "uuid-B"),
			mergeInbound("vless-in-NEW", "uuid-NEW"),
		)
		current := mergeConf(
			apiInbound(),
			mergeInbound("vless-in-A", "uuid-A", "user-a1", "user-a2"),
			mergeInbound("vless-in-B", "uuid-B"),
			mergeInbound("vless-in-GONE", "uuid-GONE", "user-g1"),
		)
		mergeRelayClients(rendered, current)

		if got, want := mergeIDs(t, rendered, "vless-in-A"), []string{"uuid-A", "user-a1", "user-a2"}; !reflect.DeepEqual(got, want) {
			t.Errorf("tenant A clients = %v, want %v", got, want)
		}
		if got, want := mergeIDs(t, rendered, "vless-in-B"), []string{"uuid-B"}; !reflect.DeepEqual(got, want) {
			t.Errorf("tenant B clients = %v, want %v", got, want)
		}
		if got, want := mergeIDs(t, rendered, "vless-in-NEW"), []string{"uuid-NEW"}; !reflect.DeepEqual(got, want) {
			t.Errorf("new tenant clients = %v, want %v", got, want)
		}
		if got, want := mergeTags(rendered), []string{"api", "vless-in-A", "vless-in-B", "vless-in-NEW"}; !reflect.DeepEqual(got, want) {
			t.Errorf("inbound tags = %v, want %v (un-enrolled tenant must not be resurrected)", got, want)
		}
		if !reflect.DeepEqual(rendered["inbounds"].([]interface{})[0], apiInbound()) {
			t.Errorf("api inbound was modified: %v", rendered["inbounds"].([]interface{})[0])
		}
	})

	t.Run("a user already present is not duplicated and the email is kept", func(t *testing.T) {
		rendered := mergeConf(mergeInbound("vless-in-A", "uuid-A"))
		cur := mergeInbound("vless-in-A", "uuid-A", "user-a1")
		cur["settings"].(map[string]interface{})["clients"].([]interface{})[1].(map[string]interface{})["email"] = "alice"
		mergeRelayClients(rendered, mergeConf(cur))

		if got, want := mergeIDs(t, rendered, "vless-in-A"), []string{"uuid-A", "user-a1"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("clients = %v, want %v", got, want)
		}
		_, clients, _ := relayClients(rendered, "vless-in-A")
		if email := clients[1].(map[string]interface{})["email"]; email != "alice" {
			t.Errorf("carried-over client email = %v, want alice", email)
		}
	})

	t.Run("clients without an id string are skipped", func(t *testing.T) {
		rendered := mergeConf(mergeInbound("vless-in-A", "uuid-A"))
		cur := mergeInbound("vless-in-A", "uuid-A", "user-a1")
		settings := cur["settings"].(map[string]interface{})
		settings["clients"] = append(settings["clients"].([]interface{}),
			map[string]interface{}{"email": "no-id"},
			map[string]interface{}{"id": 42},
			"not-a-map",
		)
		mergeRelayClients(rendered, mergeConf(cur))
		if got, want := mergeIDs(t, rendered, "vless-in-A"), []string{"uuid-A", "user-a1"}; !reflect.DeepEqual(got, want) {
			t.Errorf("clients = %v, want %v", got, want)
		}
	})

	t.Run("nil or empty current leaves rendered unchanged", func(t *testing.T) {
		for name, current := range map[string]map[string]interface{}{
			"nil":         nil,
			"empty":       {},
			"no inbounds": {"inbounds": []interface{}{}},
		} {
			rendered := mergeConf(apiInbound(), mergeInbound("vless-in-A", "uuid-A"))
			before, _ := json.Marshal(rendered)
			mergeRelayClients(rendered, current)
			after, _ := json.Marshal(rendered)
			if string(before) != string(after) {
				t.Errorf("%s current changed rendered:\nbefore %s\nafter  %s", name, before, after)
			}
		}
	})
}
