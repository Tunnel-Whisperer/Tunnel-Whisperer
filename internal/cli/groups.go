package cli

import "github.com/spf13/cobra"

// serverCmd is the top-level group for all server-mode operations.
var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Server-mode commands",
}

// serverUserCmd is the subgroup under serverCmd for user management.
var serverUserCmd = &cobra.Command{
	Use:   "user",
	Short: "Manage client users",
}

func init() {
	serverCmd.AddCommand(serverUserCmd)
	rootCmd.AddCommand(serverCmd)
}
