package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var editCmd = &cobra.Command{
	Use:   "edit",
	Short: "Edit resources",
}

var editUserCmd = &cobra.Command{
	Use:   "user <name>",
	Short: "Edit a user's port mappings",
	Args:  cobra.ExactArgs(1),
	RunE:  runEditUser,
}

func init() {
	editCmd.AddCommand(editUserCmd)
	rootCmd.AddCommand(editCmd)
}

func runEditUser(cmd *cobra.Command, args []string) error {
	if err := requireMode("server"); err != nil {
		return err
	}
	name := args[0]

	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	// Find the user and show current mappings.
	users, err := o.ListUsers()
	if err != nil {
		return fmt.Errorf("listing users: %w", err)
	}
	var found *ops.UserInfo
	for i, u := range users {
		if u.Name == name {
			found = &users[i]
			break
		}
	}
	if found == nil {
		return fmt.Errorf("user %q not found", name)
	}

	fmt.Println()
	fmt.Printf("=== Edit Port Mappings: %s ===\n", name)
	fmt.Println()

	if len(found.Tunnels) > 0 {
		fmt.Println("  Current mappings:")
		for _, t := range found.Tunnels {
			fmt.Printf("    localhost:%d → %s:%d\n", t.LocalPort, t.RemoteHost, t.RemotePort)
		}
		fmt.Println()
	}

	fmt.Println("  Enter new mappings. Empty client port to finish.")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	var mappings []config.PortMapping

	for i := 1; ; i++ {
		fmt.Printf("  Mapping %d:\n", i)
		fmt.Printf("    Client local port: ")
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

		fmt.Printf("    Server port:       ")
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
		fmt.Printf("    → localhost:%d (client) → 127.0.0.1:%d (server)\n", clientPort, serverPort)
		fmt.Println()
	}

	if err := o.UpdateUserMappings(name, mappings); err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("  Port mappings updated for %q.\n", name)
	fmt.Println("  The user needs to re-download their config for changes to take effect.")
	fmt.Println()

	return nil
}
