package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var userSingleSessionCmd = &cobra.Command{
	Use:   "single-session <name> [on|off]",
	Short: "Show or set single-session enforcement for a user",
	Long: `Show or set single-session enforcement for a user.

With single-session on, the server rejects a second concurrent SSH
connection using the same user's key. Changes rewrite the user's
authorized_keys entry and take effect on the next auth attempt — no
server restart needed.

Examples:
  tw server user single-session alice        Show the current state
  tw server user single-session alice on     Enforce one session
  tw server user single-session alice off    Allow concurrent sessions`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runUserSingleSession,
}

func init() {
	serverUserCmd.AddCommand(userSingleSessionCmd)
}

func parseOnOff(s string) (bool, error) {
	switch s {
	case "on":
		return true, nil
	case "off":
		return false, nil
	}
	return false, fmt.Errorf("expected 'on' or 'off', got %q", s)
}

func runUserSingleSession(cmd *cobra.Command, args []string) error {
	if err := requireMode("server"); err != nil {
		return err
	}
	o, err := ops.New()
	if err != nil {
		return err
	}
	name := args[0]

	if len(args) == 1 {
		users, err := o.ListUsers()
		if err != nil {
			return err
		}
		for _, u := range users {
			if u.Name == name {
				state := "off"
				if u.SingleSession {
					state = "on"
				}
				fmt.Printf("  Single-session for %q: %s\n", name, state)
				return nil
			}
		}
		return fmt.Errorf("user %q not found", name)
	}

	enabled, err := parseOnOff(args[1])
	if err != nil {
		return err
	}
	if err := o.SetUserSingleSession(name, enabled); err != nil {
		return err
	}
	state := "off"
	if enabled {
		state = "on"
	}
	fmt.Printf("  Single-session for %q: %s (authorized_keys updated)\n", name, state)
	return nil
}
