package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/api"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var listUsersCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured users",
	RunE:  runListUsers,
}

func init() {
	serverUserCmd.AddCommand(listUsersCmd)
}

func runListUsers(cmd *cobra.Command, args []string) error {
	if err := requireMode("server"); err != nil {
		return err
	}
	cfg, _ := config.Load()
	addr := fmt.Sprintf("localhost:%d", cfg.Server.APIPort)

	client, err := api.Dial(addr)
	if err != nil {
		return runListUsersLocal()
	}
	defer client.Close()

	resp, err := client.ListUsers(context.Background())
	if err != nil {
		return fmt.Errorf("listing users: %w", err)
	}

	printUsers(resp.Users)
	return nil
}

func runListUsersLocal() error {
	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	users, err := o.ListUsers()
	if err != nil {
		return err
	}

	printUsers(users)
	return nil
}

func printUsers(users []ops.UserInfo) {
	if len(users) == 0 {
		fmt.Println("  No users configured.")
		return
	}

	fmt.Println()
	for _, u := range users {
		fmt.Printf("  %s\n", u.Name)
		if u.UUID != "" {
			fmt.Printf("    UUID: %s\n", u.UUID)
		}
		for _, t := range u.Tunnels {
			fmt.Printf("    Tunnel: localhost:%d → %s:%d\n", t.LocalPort, t.RemoteHost, t.RemotePort)
		}
	}
	fmt.Println()
}
