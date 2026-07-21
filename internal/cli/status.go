package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/api"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

// relayStatusCmd is the admin-mode status command; gated to admin.
var relayStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current relay and tunnel status",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireMode("admin"); err != nil {
			return err
		}
		return sharedStatus()
	},
}

// serverStatusCmd is the server-mode status command; gated to server.
var serverStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current relay and tunnel status",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireMode("server"); err != nil {
			return err
		}
		return sharedStatus()
	},
}

// clientStatusCmd is the client-mode status command; gated to client.
var clientStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current relay and tunnel status",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireMode("client"); err != nil {
			return err
		}
		return sharedStatus()
	},
}

func init() {
	relayCmd.AddCommand(relayStatusCmd)
	serverCmd.AddCommand(serverStatusCmd)
	clientCmd.AddCommand(clientStatusCmd)
}

// sharedStatus contains the shared status logic used by all three role variants.
func sharedStatus() error {
	cfg, _ := config.Load()
	addr := fmt.Sprintf("localhost:%d", cfg.Server.APIPort)

	client, err := api.Dial(addr)
	if err != nil {
		return runStatusLocal()
	}
	defer client.Close()
	return runStatusRemote(client)
}

func runStatusRemote(client *api.Client) error {
	resp, err := client.GetStatus(context.Background())
	if err != nil {
		return fmt.Errorf("getting status: %w", err)
	}

	if w := daemonContextMismatch(resp.Mode, resp.Relay.Domain); w != "" {
		fmt.Println(w)
		fmt.Println()
	}

	fmt.Printf("  Mode:   %s\n", orDash(resp.Mode))
	fmt.Printf("  Users:  %d\n", resp.UserCount)
	fmt.Println()

	fmt.Println("  Relay:")
	fmt.Printf("    Provisioned: %v\n", resp.Relay.Provisioned)
	if resp.Relay.Provisioned {
		fmt.Printf("    Domain:      %s\n", resp.Relay.Domain)
		fmt.Printf("    IP:          %s\n", resp.Relay.IP)
		fmt.Printf("    Provider:    %s\n", resp.Relay.Provider)
	}

	if resp.Server != nil {
		fmt.Println()
		fmt.Println("  Server:")
		fmt.Printf("    State:   %s\n", resp.Server.State)
		fmt.Printf("    SSH:     %v\n", resp.Server.SSH)
		fmt.Printf("    Xray:    %v\n", resp.Server.Xray)
		fmt.Printf("    Tunnel:  %v\n", resp.Server.Tunnel)
		if resp.Server.TunnelError != "" {
			fmt.Printf("    Error:   %s\n", resp.Server.TunnelError)
		}
	}

	if resp.Client != nil {
		fmt.Println()
		fmt.Println("  Client:")
		fmt.Printf("    State:   %s\n", resp.Client.State)
		fmt.Printf("    Xray:    %v\n", resp.Client.Xray)
		fmt.Printf("    Tunnel:  %v\n", resp.Client.Tunnel)
		if resp.Client.TunnelError != "" {
			fmt.Printf("    Error:   %s\n", resp.Client.TunnelError)
		}
	}

	return nil
}

func runStatusLocal() error {
	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	mode := o.Mode()
	relay := o.GetRelayStatus()
	users, _ := o.ListUsers()

	fmt.Printf("  Mode:   %s\n", orDash(mode))
	fmt.Printf("  Users:  %d\n", len(users))
	fmt.Println()

	fmt.Println("  Relay:")
	fmt.Printf("    Provisioned: %v\n", relay.Provisioned)
	if relay.Provisioned {
		fmt.Printf("    Domain:      %s\n", relay.Domain)
		fmt.Printf("    IP:          %s\n", relay.IP)
		fmt.Printf("    Provider:    %s\n", relay.Provider)
	}

	if mode == "server" || mode == "client" {
		fmt.Println()
		fmt.Println("  (daemon not running — start with `tw server start` or `tw dashboard`)")
	}

	return nil
}

// daemonContextMismatch returns a warning when a running tw service's mode or
// relay differs from the active on-disk context, or "" when they agree. The
// service keeps serving the config it loaded at startup, so a `tw config
// use-context` switch does not reach the daemon until it is restarted; this
// surfaces that drift (the source of the "I'm in context X but status shows Y"
// confusion) instead of letting it pass silently.
func daemonContextMismatch(daemonMode, daemonRelay string) string {
	cfg, err := config.Load()
	if err != nil {
		return ""
	}
	var diffs []string
	if cfg.Mode != "" && daemonMode != "" && cfg.Mode != daemonMode {
		diffs = append(diffs, fmt.Sprintf("mode    — active context: %s, running service: %s", cfg.Mode, daemonMode))
	}
	if cfg.Xray.RelayHost != "" && daemonRelay != "" && cfg.Xray.RelayHost != daemonRelay {
		diffs = append(diffs, fmt.Sprintf("relay   — active context: %s, running service: %s", cfg.Xray.RelayHost, daemonRelay))
	}
	if len(diffs) == 0 {
		return ""
	}
	return "  ⚠ The running tw service does not match the active context:\n    " +
		strings.Join(diffs, "\n    ") +
		"\n    Restart the service to apply the switch (Windows: Restart-Service tw; otherwise: tw service stop && tw service start)."
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
