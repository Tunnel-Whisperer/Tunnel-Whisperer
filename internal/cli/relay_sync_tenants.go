package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var relaySyncTenantsCmd = &cobra.Command{
	Use:   "sync-tenants",
	Short: "Move enrolled servers to their own locked-down relay accounts",
	Long: "Updates tw's lines in the relay admin account's authorized_keys (other keys are kept), installs or updates\n" +
		"the tenant request handler, and creates, updates or removes one relay account per enrolled\n" +
		"server. Enroll and un-enroll do the same; run this once to migrate a relay whose servers\n" +
		"still log in as the admin account.",
	Args: cobra.NoArgs,
	RunE: runRelaySyncTenants,
}

func init() {
	relayCmd.AddCommand(relaySyncTenantsCmd)
}

func runRelaySyncTenants(cmd *cobra.Command, args []string) error {
	if err := requireMode("relay"); err != nil {
		return err
	}
	o, err := ops.New()
	if err != nil {
		return err
	}
	if err := o.SyncRelayTenants(cliProgress); err != nil {
		return err
	}
	fmt.Println("\n  Relay tenant accounts are in sync with the registry.")
	return nil
}
