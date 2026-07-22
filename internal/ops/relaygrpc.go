package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
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

// liveRemoveTenant removes one tenant's routing rules and inbound from the
// relay's running Xray process via gRPC, without restarting Xray — the
// counterpart of liveAddTenant. Removing the inbound severs the tenant's
// established VLESS sessions (its server transport and all its clients).
//
// Call order: rules first (they reference the inbound's tag), then the
// inbound. "not found" errors are tolerated and logged — the tenant may
// already be gone live after an earlier partial un-enroll — so re-running
// is idempotent. Any other gRPC error fails the call.
func liveRemoveTenant(client *gossh.Client, serverID string) error {
	conn, err := dialRelayGRPC(client)
	if err != nil {
		return err
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rs := routercmd.NewRoutingServiceClient(conn)
	for _, ruleTag := range []string{"allow-" + serverID, "deny-" + serverID} {
		if _, err := rs.RemoveRule(ctx, &routercmd.RemoveRuleRequest{RuleTag: ruleTag}); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "not found") {
				slog.Warn("relay xray rule already gone", "ruleTag", ruleTag)
				continue
			}
			return fmt.Errorf("RemoveRule %s: %w", ruleTag, err)
		}
	}

	hs := proxymanCmd.NewHandlerServiceClient(conn)
	inboundTag := "vless-in-" + serverID
	if _, err := hs.RemoveInbound(ctx, &proxymanCmd.RemoveInboundRequest{Tag: inboundTag}); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			slog.Warn("relay xray inbound already gone", "tag", inboundTag)
			return nil
		}
		return fmt.Errorf("RemoveInbound %s: %w", inboundTag, err)
	}
	return nil
}
