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

var deleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete resources",
}

var deleteUserCmd = &cobra.Command{
	Use:   "user <name>",
	Short: "Delete a user",
	Args:  cobra.ExactArgs(1),
	RunE:  runDeleteUser,
}

func init() {
	deleteCmd.AddCommand(deleteUserCmd)
	rootCmd.AddCommand(deleteCmd)
}

func runDeleteUser(cmd *cobra.Command, args []string) error {
	if err := requireMode("server"); err != nil {
		return err
	}
	name := args[0]

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Printf("  Delete user %q? [y/N]: ", name)
	scanner.Scan()
	if answer := strings.TrimSpace(strings.ToLower(scanner.Text())); answer != "y" {
		fmt.Println("  Aborted.")
		return nil
	}

	cfg, _ := config.Load()
	addr := fmt.Sprintf("localhost:%d", cfg.Server.APIPort)

	client, err := api.Dial(addr)
	if err != nil {
		// No daemon running, delete locally.
		o, err := ops.New()
		if err != nil {
			return fmt.Errorf("initializing: %w", err)
		}
		if err := o.DeleteUser(name); err != nil {
			return err
		}
	} else {
		defer client.Close()
		if err := client.DeleteUser(context.Background(), name); err != nil {
			return fmt.Errorf("deleting user: %w", err)
		}
	}

	fmt.Printf("  User %q deleted.\n", name)
	return nil
}
