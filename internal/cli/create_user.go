package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var createUserFrom string

var createUserCmd = &cobra.Command{
	Use:   "user",
	Short: "Create a client user with tunnel access",
	RunE:  runCreateUser,
}

func init() {
	createUserCmd.Flags().StringVar(&createUserFrom, "from", "", "copy port mappings from an existing user")
	createCmd.AddCommand(createUserCmd)
}

func runCreateUser(cmd *cobra.Command, args []string) error {
	if err := requireMode("server"); err != nil {
		return err
	}
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println()
	fmt.Println("=== Tunnel Whisperer — Create User ===")
	fmt.Println()

	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	// ── Step 1: User Name ──────────────────────────────────────────────
	fmt.Println("[1/5] User name")
	fmt.Print("      Name: ")
	scanner.Scan()
	userName := strings.TrimSpace(scanner.Text())
	if userName == "" {
		return fmt.Errorf("user name is required")
	}
	fmt.Println()

	// ── Step 2: Port Mappings ──────────────────────────────────────────
	var mappings []config.PortMapping

	if createUserFrom != "" {
		// Copy mappings from an existing user.
		fmt.Printf("[2/5] Copying port mappings from user %q\n", createUserFrom)
		users, err := o.ListUsers()
		if err != nil {
			return fmt.Errorf("listing users: %w", err)
		}
		var found bool
		for _, u := range users {
			if u.Name == createUserFrom {
				for _, t := range u.Tunnels {
					mappings = append(mappings, config.PortMapping{ClientPort: t.LocalPort, ServerPort: t.RemotePort})
				}
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("user %q not found", createUserFrom)
		}
		if len(mappings) == 0 {
			return fmt.Errorf("user %q has no port mappings", createUserFrom)
		}
		for _, m := range mappings {
			fmt.Printf("      → localhost:%d (client) → 127.0.0.1:%d (server)\n", m.ClientPort, m.ServerPort)
		}
		fmt.Println()
	} else {
		fmt.Println("[2/5] Port mappings")
		fmt.Println("      Map client local ports to server ports (localhost only).")
		fmt.Println("      Enter mappings one at a time. Empty client port to finish.")
		fmt.Println()

		for i := 1; ; i++ {
			fmt.Printf("      Mapping %d:\n", i)
			fmt.Printf("        Client local port: ")
			scanner.Scan()
			clientPortStr := strings.TrimSpace(scanner.Text())
			if clientPortStr == "" {
				if len(mappings) == 0 {
					return fmt.Errorf("at least one port mapping is required")
				}
				break
			}
			clientPort, err := strconv.Atoi(clientPortStr)
			if err != nil || clientPort < 1 || clientPort > 65535 {
				return fmt.Errorf("invalid port: %s", clientPortStr)
			}

			fmt.Printf("        Server port:       ")
			scanner.Scan()
			serverPortStr := strings.TrimSpace(scanner.Text())
			if serverPortStr == "" {
				return fmt.Errorf("server port is required")
			}
			serverPort, err := strconv.Atoi(serverPortStr)
			if err != nil || serverPort < 1 || serverPort > 65535 {
				return fmt.Errorf("invalid port: %s", serverPortStr)
			}

			mappings = append(mappings, config.PortMapping{ClientPort: clientPort, ServerPort: serverPort})
			fmt.Printf("        → localhost:%d (client) → 127.0.0.1:%d (server)\n", clientPort, serverPort)
			fmt.Println()
		}
		fmt.Println()
	}

	req := ops.CreateUserRequest{
		Name:     userName,
		Mappings: mappings,
	}

	if err := o.CreateUser(context.Background(), req, cliProgress); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("=== User created ===")
	fmt.Println()
	fmt.Println("  Send the user's config directory to the client.")
	fmt.Println("  The client places these files in their config directory and runs `tw connect`.")
	fmt.Println()

	return nil
}
