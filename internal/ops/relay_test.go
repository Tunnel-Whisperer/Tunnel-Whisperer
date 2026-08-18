package ops

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

// TestRenderRelayConfigsIncludesEnrollRoute is a regression test for the
// fresh-relay deadlock: renderRelayConfigs (the provisioning-path render
// shared by RelayInstall/ProvisionRelay) built its own caddy.Server{...}
// literal WITHOUT EnrollTok/EnrollPort, unlike relayTenantState's literal —
// so a freshly-provisioned relay's admin enroll route rendered as the dead
// `path /enroll//*` (empty token segment, matches nothing), deadlocking the
// very first invite of any kind (which can only reach the enroll-populating
// EnrollServer call by going *through* that route first).
func TestRenderRelayConfigsIncludesEnrollRoute(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o, err := New()
	if err != nil {
		t.Fatal(err)
	}
	o.cfg.Xray.UUID = "a1b2c3d4-aaaa-bbbb-cccc-ddddeeeeffff"
	o.cfg.Xray.RelayHost = "relay.example.com"
	o.cfg.Server.RemotePort = 20000

	_, caddyfileB64, _, err := o.renderRelayConfigs(o.cfg)
	if err != nil {
		t.Fatalf("renderRelayConfigs: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(caddyfileB64)
	if err != nil {
		t.Fatalf("decoding Caddyfile: %v", err)
	}
	caddyfile := string(raw)

	wantTok := first8(o.cfg.Xray.UUID)
	wantPort := enrollPort(o.cfg.Server.RemotePort)

	wantRoute := fmt.Sprintf("path /enroll/%s/*", wantTok)
	if !strings.Contains(caddyfile, wantRoute) {
		t.Errorf("rendered Caddyfile missing enroll route %q:\n%s", wantRoute, caddyfile)
	}
	wantProxy := fmt.Sprintf("reverse_proxy 127.0.0.1:%d", wantPort)
	if !strings.Contains(caddyfile, wantProxy) {
		t.Errorf("rendered Caddyfile missing enroll reverse_proxy %q:\n%s", wantProxy, caddyfile)
	}
	if strings.Contains(caddyfile, "path /enroll//*") {
		t.Errorf("rendered Caddyfile contains the dead empty-token enroll route:\n%s", caddyfile)
	}
}
