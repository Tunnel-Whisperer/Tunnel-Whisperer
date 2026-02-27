package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/api"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var destroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Destroy infrastructure resources",
}

var destroyRelayServerCmd = &cobra.Command{
	Use:   "relay-server",
	Short: "Destroy the provisioned relay server",
	RunE:  runDestroyRelayServer,
}

func init() {
	destroyCmd.AddCommand(destroyRelayServerCmd)
	rootCmd.AddCommand(destroyCmd)
}

func runDestroyRelayServer(cmd *cobra.Command, args []string) error {
	if err := requireMode("server"); err != nil {
		return err
	}
	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	status := o.GetRelayStatus()
	if !status.Provisioned {
		fmt.Println("  No relay is currently provisioned.")
		return nil
	}

	fmt.Println()
	fmt.Printf("  Relay:    %s\n", status.Domain)
	fmt.Printf("  Provider: %s\n", status.Provider)
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)

	// Collect credentials if AWS.
	var creds map[string]string
	if status.Provider == "AWS" {
		fmt.Println("  AWS credentials needed to destroy resources.")
		fmt.Print("  AWS Access Key ID: ")
		scanner.Scan()
		keyID := strings.TrimSpace(scanner.Text())
		fmt.Print("  AWS Secret Access Key: ")
		scanner.Scan()
		secret := strings.TrimSpace(scanner.Text())
		if keyID == "" || secret == "" {
			return fmt.Errorf("both AWS Access Key ID and Secret Access Key are required")
		}
		creds = map[string]string{
			"AWS_ACCESS_KEY_ID":     keyID,
			"AWS_SECRET_ACCESS_KEY": secret,
		}
		fmt.Println()
	}

	fmt.Print("  Destroy this relay? [y/N]: ")
	scanner.Scan()
	if answer := strings.TrimSpace(strings.ToLower(scanner.Text())); answer != "y" {
		fmt.Println("  Aborted.")
		return nil
	}
	fmt.Println()

	cfg, _ := config.Load()
	addr := fmt.Sprintf("localhost:%d", cfg.Server.APIPort)

	client, dialErr := api.Dial(addr)
	if dialErr != nil {
		// No daemon running, destroy locally.
		if err := o.DestroyRelay(context.Background(), creds, cliProgress); err != nil {
			return err
		}
	} else {
		defer client.Close()
		fmt.Println("  Destroying via daemon...")
		if err := client.DestroyRelay(context.Background(), creds); err != nil {
			return fmt.Errorf("destroying relay: %w", err)
		}
	}

	fmt.Println()
	fmt.Println("  Relay destroyed.")
	return nil
}
