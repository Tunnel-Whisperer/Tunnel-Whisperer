package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	proxymanCmd "github.com/xtls/xray-core/app/proxyman/command"
	routercmd "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/common/serial"
	conf "github.com/xtls/xray-core/infra/conf"
	gossh "golang.org/x/crypto/ssh"

	relayxray "github.com/tunnelwhisperer/tw/internal/relay/xray"
)

// liveAddTenant adds one tenant's inbound and routing rules to the relay's
// running Xray process via gRPC, without restarting Xray.
//
// Call order: AddInbound first (the rules reference the inbound's tag), then
// AddRule (ShouldAppend:true — appended before the catch-all deny).
//
// Idempotency: AddRule errors on a duplicate ruleTag. The caller (B5) is
// responsible for removing existing rules before re-enrolling a tenant;
// liveAddTenant only handles the first-join path.
func liveAddTenant(client *gossh.Client, t relayxray.Tenant) error {
	conn, err := dialRelayGRPC(client)
	if err != nil {
		return err
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// -- inbound --
	inJSON, err := relayxray.RenderTenantInbound(t)
	if err != nil {
		return err
	}
	var inDetour conf.InboundDetourConfig
	if err := json.Unmarshal([]byte(inJSON), &inDetour); err != nil {
		return fmt.Errorf("parsing tenant inbound: %w", err)
	}
	inHandler, err := inDetour.Build()
	if err != nil {
		return fmt.Errorf("building inbound: %w", err)
	}
	hs := proxymanCmd.NewHandlerServiceClient(conn)
	if _, err := hs.AddInbound(ctx, &proxymanCmd.AddInboundRequest{Inbound: inHandler}); err != nil {
		return fmt.Errorf("AddInbound %s: %w", t.ServerID, err)
	}

	// -- routing rules --
	rulesJSON, err := relayxray.RenderTenantRules(t)
	if err != nil {
		return err
	}
	var routerCfg conf.RouterConfig
	if err := json.Unmarshal([]byte(rulesJSON), &routerCfg); err != nil {
		return fmt.Errorf("parsing tenant rules: %w", err)
	}
	rc, err := routerCfg.Build()
	if err != nil {
		return fmt.Errorf("building rules: %w", err)
	}
	rs := routercmd.NewRoutingServiceClient(conn)
	if _, err := rs.AddRule(ctx, &routercmd.AddRuleRequest{Config: serial.ToTypedMessage(rc), ShouldAppend: true}); err != nil {
		return fmt.Errorf("AddRule %s: %w", t.ServerID, err)
	}

	return nil
}
