package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/api"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

// testRelayCmd is the admin-mode relay test; gated to admin.
var testRelayCmd = &cobra.Command{
	Use:   "test",
	Short: "Test connectivity to the relay server",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireMode("admin"); err != nil {
			return err
		}
		return sharedTestRelay()
	},
}

// serverTestCmd is the server-mode relay test; gated to server.
var serverTestCmd = &cobra.Command{
	Use:   "test",
	Short: "Test connectivity to the relay server",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireMode("server"); err != nil {
			return err
		}
		return sharedTestRelay()
	},
}

// clientTestCmd is the client-mode relay test; gated to client.
var clientTestCmd = &cobra.Command{
	Use:   "test",
	Short: "Test connectivity to the relay server",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireMode("client"); err != nil {
			return err
		}
		return sharedTestRelay()
	},
}

func init() {
	relayCmd.AddCommand(testRelayCmd)
	serverCmd.AddCommand(serverTestCmd)
	clientCmd.AddCommand(clientTestCmd)
}

// sharedTestRelay contains the shared relay-test logic used by all three role
// variants (admin, server, client).
func sharedTestRelay() error {
	cfg, _ := config.Load()
	addr := fmt.Sprintf("localhost:%d", cfg.Server.APIPort)

	client, err := api.Dial(addr)
	if err != nil {
		return runTestRelayLocal()
	}
	defer client.Close()
	return runTestRelayRemote(client)
}

func runTestRelayRemote(client *api.Client) error {
	fmt.Println()
	fmt.Println("  Testing relay (via daemon)...")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := client.TestRelay(ctx)
	if err != nil {
		return fmt.Errorf("test relay: %w", err)
	}

	for _, step := range resp.Steps {
		if step.Status == "completed" {
			msg := step.Message
			if msg == "" {
				msg = "✓"
			}
			fmt.Printf("      %s — %s\n", step.Label, msg)
		} else if step.Status == "failed" {
			fmt.Printf("      %s ✗ %s\n", step.Label, step.Error)
		}
	}

	fmt.Println()
	return nil
}

func runTestRelayLocal() error {
	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	status := o.GetRelayStatus()
	if !status.Provisioned {
		return fmt.Errorf("no relay provisioned — run `tw relay create` first")
	}

	fmt.Println()
	fmt.Printf("  Testing relay: %s\n", status.Domain)
	fmt.Println()

	o.TestRelay(cliProgress)

	fmt.Println()
	return nil
}
