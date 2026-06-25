package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var adminReconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Re-enroll the relay onto the current multi-tenant identity scheme",
	RunE:  runAdminReconcile,
}

func init() {
	adminCmd.AddCommand(adminReconcileCmd)
}

func runAdminReconcile(cmd *cobra.Command, args []string) error {
	if err := requireMode("admin"); err != nil {
		return err
	}
	o, err := ops.New()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}
	status := o.GetRelayStatus()
	if !status.Provisioned {
		return fmt.Errorf("no relay provisioned — run `tw admin create` first")
	}
	return o.ReconcileRelay(cliProgress)
}
