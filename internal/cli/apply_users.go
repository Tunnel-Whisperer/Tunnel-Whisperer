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

var applyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply resources to the relay",
}

var applyUsersCmd = &cobra.Command{
	Use:   "users [name...]",
	Short: "Register users on the relay",
	Long:  "Register users on the relay. Specify user names, or omit to apply all.",
	RunE:  runApplyUsers,
}

var unregisterCmd = &cobra.Command{
	Use:   "unregister",
	Short: "Remove resources from the relay",
}

var unregisterUserCmd = &cobra.Command{
	Use:   "user <name>",
	Short: "Unregister a user from the relay",
	Args:  cobra.ExactArgs(1),
	RunE:  runUnregisterUser,
}

func init() {
	applyCmd.AddCommand(applyUsersCmd)
	unregisterCmd.AddCommand(unregisterUserCmd)
	rootCmd.AddCommand(applyCmd)
	rootCmd.AddCommand(unregisterCmd)
}

func runApplyUsers(cmd *cobra.Command, args []string) error {
	if err := requireMode("server"); err != nil {
		return err
	}

	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	names := args // empty means all

	fmt.Println()
	if len(names) == 0 {
		fmt.Println("  Registering all users on the relay...")
	} else {
		fmt.Printf("  Registering %s on the relay...\n", strings.Join(names, ", "))
	}
	fmt.Println()

	if err := o.ApplyUsers(context.Background(), names, cliProgress); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("  Done.")
	fmt.Println()
	return nil
}

func runUnregisterUser(cmd *cobra.Command, args []string) error {
	if err := requireMode("server"); err != nil {
		return err
	}
	name := args[0]

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Printf("  Unregister %q from the relay? They will lose tunnel access until re-registered. [y/N]: ", name)
	scanner.Scan()
	if answer := strings.TrimSpace(strings.ToLower(scanner.Text())); answer != "y" {
		fmt.Println("  Aborted.")
		return nil
	}

	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	fmt.Println()
	if err := o.UnregisterUsers(context.Background(), []string{name}, cliProgress); err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("  User %q unregistered from the relay.\n", name)
	fmt.Println()
	return nil
}
