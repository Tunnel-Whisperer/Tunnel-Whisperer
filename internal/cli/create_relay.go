package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create infrastructure resources",
}

var createRelayServerCmd = &cobra.Command{
	Use:   "relay-server",
	Short: "Interactively provision a relay server on a cloud provider",
	RunE:  runCreateRelayServer,
}

func init() {
	createCmd.AddCommand(createRelayServerCmd)
	rootCmd.AddCommand(createCmd)
}

// cliProgress prints ProgressEvents to stdout.
func cliProgress(e ops.ProgressEvent) {
	prefix := fmt.Sprintf("[%d/%d] %s", e.Step, e.Total, e.Label)
	switch e.Status {
	case "running":
		if e.Message != "" {
			fmt.Printf("      %s... %s\n", prefix, e.Message)
		} else {
			fmt.Printf("      %s...\n", prefix)
		}
	case "completed":
		if e.Message != "" {
			fmt.Printf("      %s — %s\n", prefix, e.Message)
		} else {
			fmt.Printf("      %s ✓\n", prefix)
		}
	case "failed":
		fmt.Printf("      %s ✗ %s\n", prefix, e.Error)
	}
}

func runCreateRelayServer(cmd *cobra.Command, args []string) error {
	if err := requireMode("server"); err != nil {
		return err
	}
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println()
	fmt.Println("=== Tunnel Whisperer — Relay Server Setup ===")
	fmt.Println()

	if !ops.TerraformAvailable() {
		return fmt.Errorf("terraform is required but not found in PATH\n  Install: https://developer.hashicorp.com/terraform/install")
	}

	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	cfg := o.Config()

	// Check if relay was already provisioned.
	status := o.GetRelayStatus()
	if status.Provisioned {
		fmt.Printf("  Relay already provisioned (provider: %s).\n", status.Provider)
		fmt.Print("  Destroy and recreate? [y/N]: ")
		scanner.Scan()
		if answer := strings.TrimSpace(strings.ToLower(scanner.Text())); answer != "y" {
			fmt.Println("  Aborted.")
			return nil
		}
		// Collect credentials for destroy.
		var creds map[string]string
		if status.Provider == "AWS" {
			fmt.Println("  AWS credentials needed to destroy resources.")
			fmt.Print("  AWS Access Key ID: ")
			scanner.Scan()
			keyID := strings.TrimSpace(scanner.Text())
			fmt.Print("  AWS Secret Access Key: ")
			scanner.Scan()
			secret := strings.TrimSpace(scanner.Text())
			if keyID != "" && secret != "" {
				creds = map[string]string{
					"AWS_ACCESS_KEY_ID":     keyID,
					"AWS_SECRET_ACCESS_KEY": secret,
				}
			}
		}
		fmt.Println("  Destroying existing relay resources...")
		if err := o.DestroyRelay(context.Background(), creds, cliProgress); err != nil {
			fmt.Printf("  Warning: %v\n", err)
			fmt.Println("  You may need to delete cloud resources manually.")
		}
	}

	// ── Step 3: Relay Domain ────────────────────────────────────────────
	fmt.Println("[3/9] Relay domain")
	if cfg.Xray.RelayHost != "" {
		fmt.Printf("      Current: %s\n", cfg.Xray.RelayHost)
		fmt.Print("      Keep? [Y/n]: ")
		scanner.Scan()
		if answer := strings.TrimSpace(strings.ToLower(scanner.Text())); answer == "n" {
			cfg.Xray.RelayHost = ""
		}
	}
	var domain string
	if cfg.Xray.RelayHost == "" {
		fmt.Print("      Enter relay domain (e.g. relay.example.com): ")
		scanner.Scan()
		domain = strings.TrimSpace(scanner.Text())
		if domain == "" {
			return fmt.Errorf("relay domain is required")
		}
	} else {
		domain = cfg.Xray.RelayHost
	}
	fmt.Printf("      Domain: %s\n", domain)
	fmt.Println()

	// ── Step 4: Cloud Provider ──────────────────────────────────────────
	fmt.Println("[4/9] Cloud provider")
	providers := ops.CloudProviders()
	for i, p := range providers {
		fmt.Printf("      %d) %s\n", i+1, p.Name)
	}
	fmt.Print("      Select [1-3]: ")
	scanner.Scan()
	providerIdx := strings.TrimSpace(scanner.Text())
	var selected ops.CloudProvider
	switch providerIdx {
	case "1":
		selected = providers[0]
	case "2":
		selected = providers[1]
	case "3":
		selected = providers[2]
	default:
		return fmt.Errorf("invalid choice: %s", providerIdx)
	}
	fmt.Printf("      Provider: %s\n", selected.Name)
	fmt.Println()

	// ── Step 5: Cloud Credentials ───────────────────────────────────────
	fmt.Printf("[5/9] %s credentials\n", selected.Name)
	fmt.Printf("      Generate here: %s\n", selected.TokenLink)
	fmt.Println()

	var token, awsSecretKey string
	if selected.Name == "AWS" {
		fmt.Print("      AWS Access Key ID: ")
		scanner.Scan()
		token = strings.TrimSpace(scanner.Text())
		fmt.Print("      AWS Secret Access Key: ")
		scanner.Scan()
		awsSecretKey = strings.TrimSpace(scanner.Text())
		if token == "" || awsSecretKey == "" {
			return fmt.Errorf("both AWS Access Key ID and Secret Access Key are required")
		}
	} else {
		fmt.Printf("      %s: ", selected.TokenName)
		scanner.Scan()
		token = strings.TrimSpace(scanner.Text())
		if token == "" {
			return fmt.Errorf("%s is required", selected.TokenName)
		}
	}
	fmt.Println()

	// ── Step 7: Confirm ─────────────────────────────────────────────────
	fmt.Println("[7/9] Provisioning relay")
	fmt.Printf("      Provider:  %s\n", selected.Name)
	fmt.Printf("      Domain:    %s\n", domain)
	fmt.Printf("      Instance:  Ubuntu 24.04 (smallest tier)\n")
	fmt.Printf("      Firewall:  ports 80, 443 only\n")
	fmt.Printf("      Software:  Caddy + Xray + SSH (localhost-only)\n")
	fmt.Println()
	fmt.Print("      Proceed? [Y/n]: ")
	scanner.Scan()
	if answer := strings.TrimSpace(strings.ToLower(scanner.Text())); answer == "n" {
		fmt.Println("      Aborted.")
		return nil
	}
	fmt.Println()

	req := ops.RelayProvisionRequest{
		Domain:       domain,
		ProviderKey:  selected.Key,
		ProviderName: selected.Name,
		Token:        token,
		AWSSecretKey: awsSecretKey,
	}

	if err := o.ProvisionRelay(context.Background(), req, cliProgress); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("=== Relay server setup complete ===")
	fmt.Println()
	fmt.Println("  Run `tw serve` to start the tunnel.")
	fmt.Println()

	return nil
}
