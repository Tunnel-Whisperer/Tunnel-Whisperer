package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/api"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current server/client status",
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
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
		fmt.Println("  (daemon not running — start with `tw serve` or `tw dashboard`)")
	}

	return nil
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
