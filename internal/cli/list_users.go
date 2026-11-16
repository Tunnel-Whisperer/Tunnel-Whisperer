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
		if u.Expired {
			fmt.Println("    Expires: EXPIRED")
		} else if e := formatExpiry(u.ExpiresAt, time.Now()); e != "" {
			fmt.Printf("    Expires: %s\n", e)
		}
		for _, t := range u.Tunnels {
			fmt.Printf("    Tunnel: localhost:%d → %s:%d\n", t.LocalPort, t.RemoteHost, t.RemotePort)
		}
	}
	fmt.Println()
}

// formatExpiry renders an RFC 3339 expiry for display: empty when there is
// none, "EXPIRED" once past, and a day hint when it is within a week.
func formatExpiry(expiresAt string, now time.Time) string {
	if expiresAt == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return expiresAt
	}
	remaining := t.Sub(now)
	switch {
	case remaining <= 0:
		return "EXPIRED"
	case remaining < 24*time.Hour:
		return expiresAt + " (in <1d)"
	case remaining <= 7*24*time.Hour:
		return fmt.Sprintf("%s (in %dd)", expiresAt, int(remaining/(24*time.Hour)))
	}
	return expiresAt
}
