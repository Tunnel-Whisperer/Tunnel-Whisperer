package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var createRelayServerCmd = &cobra.Command{
	Use:   "create",
	Short: "Provision a relay server on a cloud provider",
	RunE:  runCreateRelayServer,
}

func init() {
	createRelayServerCmd.Flags().Bool("ssh-open", false,
		"manual install: open the relay's SSH port (22) to the internet (default: SSH reachable only through the tunnel)")
	createRelayServerCmd.Flags().String("provider", "",
		`provider selection; currently only "manual" — with --domain and --ip, runs fully non-interactive`)
	createRelayServerCmd.Flags().String("domain", "",
		"relay domain (skips the domain prompt)")
	createRelayServerCmd.Flags().String("ip", "",
		"relay public IP (manual provider only; skips the IP prompt)")
	relayCmd.AddCommand(createRelayServerCmd)
}

// validateCreateFlags is the pure decision behind the non-interactive create
// flags: it rejects combinations that can never work and reports whether the
// flags fully specify a manual relay, i.e. the run needs no prompts at all.
func validateCreateFlags(provider, domain, ip string) (bool, error) {
	if provider != "" && provider != "manual" {
		return false, fmt.Errorf("unsupported --provider %q: non-interactive create currently supports only \"manual\"", provider)
	}
	if ip != "" && provider != "manual" {
		return false, fmt.Errorf("--ip requires --provider manual")
	}
	return provider == "manual" && domain != "" && ip != "", nil
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
	if err := requireMode("admin"); err != nil {
		return err
	}
	flagProvider, _ := cmd.Flags().GetString("provider")
	flagDomain, _ := cmd.Flags().GetString("domain")
	flagIP, _ := cmd.Flags().GetString("ip")
	nonInteractive, err := validateCreateFlags(flagProvider, flagDomain, flagIP)
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println()
	fmt.Println("=== Tunnel Whisperer — Relay Server Setup ===")
	fmt.Println()

	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	cfg := o.Config()

	// Check if relay was already provisioned. Scripted runs never destroy
	// infrastructure implicitly — they fail instead of prompting.
	status := o.GetRelayStatus()
	if status.Provisioned && nonInteractive {
		return fmt.Errorf("relay already provisioned (provider: %s) — run 'tw relay destroy' first", status.Provider)
	}
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

	// Fully flag-specified manual relay: no prompts at all. --ssh-open's
	// default (false) applies without the usual prompt.
	if nonInteractive {
		sshOpen, _ := cmd.Flags().GetBool("ssh-open")
		return runManualRelayNonInteractive(o, flagDomain, flagIP, sshOpen)
	}

	// Public SSH exposure — asked up front, applies to every provider (cloud or
	// manual). tw's own key is tunnel-only regardless (from="127.0.0.1"); this
	// only controls whether a human can reach port 22 from the internet with
	// their own credentials.
	sshOpen := resolveSSHOpen(cmd, scanner)

	// ── Step 3: Relay Domain ────────────────────────────────────────────
	fmt.Println("[3/9] Relay domain")
	var domain string
	if flagDomain != "" {
		domain = flagDomain
	} else {
		if cfg.Xray.RelayHost != "" {
			fmt.Printf("      Current: %s\n", cfg.Xray.RelayHost)
			fmt.Print("      Keep? [Y/n]: ")
			scanner.Scan()
			if answer := strings.TrimSpace(strings.ToLower(scanner.Text())); answer == "n" {
				cfg.Xray.RelayHost = ""
			}
		}
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
	}
	fmt.Printf("      Domain: %s\n", domain)
	fmt.Println()

	// ── Step 4: Cloud Provider ──────────────────────────────────────────
	fmt.Println("[4/9] Cloud provider")
	providers := ops.CloudProviders()
	manualIdx := len(providers) + 1
	var choice int
	if flagProvider == "manual" {
		choice = manualIdx
	} else {
		for i, p := range providers {
			fmt.Printf("      %d) %s\n", i+1, p.Name)
		}
		fmt.Printf("      %d) Manual (bring your own VM)\n", manualIdx)
		fmt.Printf("      Select [1-%d]: ", manualIdx)
		scanner.Scan()
		providerIdx := strings.TrimSpace(scanner.Text())
		choice, err = strconv.Atoi(providerIdx)
		if err != nil || choice < 1 || choice > manualIdx {
			return fmt.Errorf("invalid choice: %s", providerIdx)
		}
	}

	// Manual (bring-your-own-VM) relay: no Terraform, no cloud credentials.
	if choice == manualIdx {
		return runManualRelay(o, scanner, domain, flagIP, sshOpen)
	}

	if !ops.TerraformAvailable() {
		return fmt.Errorf("terraform is required but not found in PATH\n  Install: https://developer.hashicorp.com/terraform/install")
	}
	selected := providers[choice-1]
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
	if sshOpen {
		fmt.Printf("      Firewall:  ports 80, 443, 22\n")
		fmt.Printf("      Software:  Caddy + Xray + SSH (port 22 public; tw key tunnel-only)\n")
	} else {
		fmt.Printf("      Firewall:  ports 80, 443 only\n")
		fmt.Printf("      Software:  Caddy + Xray + SSH (localhost-only)\n")
	}
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
		SSHOpen:      sshOpen,
	}

	if err := o.ProvisionRelay(context.Background(), req, cliProgress); err != nil {
		return err
	}

	// Emit the relay bundle (the portable identity for this relay).
	fmt.Println()
	fmt.Println("  Creating relay bundle...")
	if err := writeProfileBundle(o, domain); err != nil {
		fmt.Printf("  Warning: could not create relay bundle: %v\n", err)
		fmt.Println("  Run `tw config export` to create it later.")
	}

	fmt.Println()
	fmt.Println("=== Relay server setup complete ===")
	fmt.Println()
	fmt.Println("  Run `tw server start` to start the tunnel.")
	fmt.Println()

	return nil
}

// resolveSSHOpen decides whether the relay's SSH port 22 is exposed to the
// internet. --ssh-open sets it non-interactively; otherwise it prompts. This is
// only about a human's own maintenance access — tw's generated key is bound to
// the tunnel (from="127.0.0.1") regardless of this choice.
func resolveSSHOpen(cmd *cobra.Command, scanner *bufio.Scanner) bool {
	if cmd.Flags().Changed("ssh-open") {
		v, _ := cmd.Flags().GetBool("ssh-open")
		fmt.Printf("[*] Public SSH: port 22 open to the internet = %t (from --ssh-open)\n\n", v)
		return v
	}
	fmt.Println("[*] Public SSH access")
	fmt.Println("      tw reaches the relay only through the encrypted tunnel; its key")
	fmt.Println("      never works over public port 22. Opening 22 is only for your own")
	fmt.Println("      maintenance login, with your own credentials (your problem to manage).")
	fmt.Print("      Open port 22 to the internet? [y/N]: ")
	scanner.Scan()
	open := strings.TrimSpace(strings.ToLower(scanner.Text())) == "y"
	fmt.Println()
	return open
}

// runManualRelay drives the bring-your-own-VM relay flow: it prints an install
// script the admin runs on their own server (no Terraform, no cloud
// credentials), then records the relay marker and emits the profile bundle.
// A non-empty flagIP pre-answers the public-IP prompt.
func runManualRelay(o *ops.Ops, scanner *bufio.Scanner, domain, flagIP string, sshOpen bool) error {
	fmt.Println("      Provider: Manual (bring your own VM)")
	fmt.Println()

	script, err := o.GenerateManualInstallScript(domain, sshOpen)
	if err != nil {
		return fmt.Errorf("generating install script: %w", err)
	}

	// Also write the script to tw-install-<domain>.sh in the current directory so
	// the admin can scp/paste it to the VM instead of copying it from the console.
	scriptPath, werr := writeManualInstallScript(domain, script)
	if werr != nil {
		fmt.Printf("      Warning: could not write install script to a file: %v\n", werr)
	}

	fmt.Println()
	fmt.Println("[7/9] Manual relay install")
	fmt.Println("      1. Create a VM (Ubuntu 24.04) with a public IP and point")
	fmt.Printf("         %s at it (DNS A record).\n", domain)
	fmt.Println("      2. Open ports 80 and 443 in its firewall.")
	fmt.Println("      3. Run the following script on the VM as root:")
	if scriptPath != "" {
		fmt.Printf("         (also saved to %s)\n", scriptPath)
	}
	fmt.Println()
	fmt.Println("----------------------------------------------------------------")
	fmt.Println(script)
	fmt.Println("----------------------------------------------------------------")
	fmt.Println()

	ip := flagIP
	if ip == "" {
		fmt.Print("      Relay public IP address: ")
		scanner.Scan()
		ip = strings.TrimSpace(scanner.Text())
		if ip == "" {
			return fmt.Errorf("relay public IP is required")
		}
	} else {
		fmt.Printf("      Relay public IP address: %s (from --ip)\n", ip)
	}

	fmt.Print("      Have you run the script on the VM? [y/N]: ")
	scanner.Scan()
	if strings.TrimSpace(strings.ToLower(scanner.Text())) != "y" {
		fmt.Println("      Aborted. Re-run once the relay is set up.")
		return nil
	}

	return finishManualRelay(o, domain, ip, sshOpen)
}

// runManualRelayNonInteractive is the fully flag-specified manual relay flow
// (`--provider manual --domain --ip`): it writes the install script and records
// the relay immediately — running the script on the VM happens afterwards.
func runManualRelayNonInteractive(o *ops.Ops, domain, ip string, sshOpen bool) error {
	fmt.Println("      Provider: Manual (bring your own VM)")
	fmt.Printf("      Domain:   %s\n", domain)
	fmt.Printf("      IP:       %s\n", ip)
	fmt.Println()

	script, err := o.GenerateManualInstallScript(domain, sshOpen)
	if err != nil {
		return fmt.Errorf("generating install script: %w", err)
	}

	scriptPath, werr := writeManualInstallScript(domain, script)
	if werr != nil {
		// Without the file there is nothing for the admin to run — print the
		// script itself as the fallback.
		fmt.Printf("      Warning: could not write install script to a file: %v\n", werr)
		fmt.Println()
		fmt.Println("----------------------------------------------------------------")
		fmt.Println(script)
		fmt.Println("----------------------------------------------------------------")
	}

	if err := finishManualRelay(o, domain, ip, sshOpen); err != nil {
		return err
	}

	fmt.Println("  The relay is recorded but not yet installed. Next steps:")
	fmt.Printf("    1. Point a DNS A record:  %s  ->  %s\n", domain, ip)
	fmt.Println("    2. Open ports 80 and 443 in the VM's firewall.")
	if scriptPath != "" {
		fmt.Printf("    3. Run %s on the VM as root.\n", scriptPath)
	} else {
		fmt.Println("    3. Run the install script above on the VM as root.")
	}
	fmt.Println()
	return nil
}

// finishManualRelay records the manual relay marker and emits the profile
// bundle — the shared tail of the wizard and non-interactive flows.
func finishManualRelay(o *ops.Ops, domain, ip string, sshOpen bool) error {
	if err := o.SaveManualRelay(domain, ip, sshOpen); err != nil {
		return fmt.Errorf("recording manual relay: %w", err)
	}

	// Emit the relay bundle (the portable identity for this relay).
	fmt.Println()
	fmt.Println("  Creating relay bundle...")
	if err := writeProfileBundle(o, domain); err != nil {
		fmt.Printf("  Warning: could not create relay bundle: %v\n", err)
		fmt.Println("  Run `tw config export` to create it later.")
	}

	fmt.Println()
	fmt.Println("=== Relay server setup complete ===")
	fmt.Println()
	fmt.Println("  Run `tw server start` to start the tunnel.")
	fmt.Println()

	return nil
}

// writeManualInstallScript writes the manual-install bash script to
// tw-install-<domain>.sh in the current directory and returns its absolute path.
// The script carries the tunnel UUID, so it is written 0700 (owner-only).
func writeManualInstallScript(domain, script string) (string, error) {
	safe := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-").Replace(domain)
	if safe == "" {
		safe = "relay"
	}
	fname := fmt.Sprintf("tw-install-%s.sh", safe)
	if err := os.WriteFile(fname, []byte(script), 0700); err != nil {
		return "", fmt.Errorf("writing %s: %w", fname, err)
	}
	abs, err := filepath.Abs(fname)
	if err != nil {
		return fname, nil
	}
	return abs, nil
}
