package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var (
	serverJoinApply      string
	serverJoinNewContext string
)

var serverJoinCmd = &cobra.Command{
	Use:   "join-relay <relay-host>",
	Short: "Join this server to a relay: generate a join-request for its admin, then --apply the enrollment response",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runServerJoin,
}

func init() {
	serverJoinCmd.Flags().StringVar(&serverJoinApply, "apply", "", "apply an admin join-response file instead of generating a request")
	serverJoinCmd.Flags().StringVar(&serverJoinNewContext, "new-context", "", "first create+switch to a fresh context of this name (preserving the current one), then join in it")
	serverCmd.AddCommand(serverJoinCmd)
}

func runServerJoin(cmd *cobra.Command, args []string) error {
	o, err := ops.New()
	if err != nil {
		return err
	}
	if serverJoinNewContext != "" {
		if err := newContextSealingCurrent(o, serverJoinNewContext); err != nil {
			return err
		}
		fmt.Printf("  Created context %q (current one preserved); joining in it.\n", serverJoinNewContext)
	}
	if serverJoinApply != "" {
		data, err := os.ReadFile(serverJoinApply)
		if err != nil {
			return fmt.Errorf("reading response: %w", err)
		}
		resp, err := ops.DecodeJoinResponse(data)
		if err != nil {
			return err
		}
		if err := o.ApplyJoinResponse(resp); err != nil {
			return err
		}
		fmt.Printf("  Joined relay %s on %s. Start the tunnel with: tw server start\n", resp.RelayHost, resp.Path)
		return nil
	}
	if len(args) != 1 {
		return fmt.Errorf("relay host required: tw server join-relay <relay-host>")
	}
	req, err := o.GenerateJoinRequest(args[0])
	if err != nil {
		return err
	}
	data, err := req.Encode()
	if err != nil {
		return err
	}
	fname := fmt.Sprintf("tw_join_%s.json", req.ServerID)
	if err := os.WriteFile(fname, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", fname, err)
	}
	fmt.Printf("  Join request written: %s\n", fname)
	fmt.Println("  Send it to the relay admin, who runs: tw relay enroll-server <this file>")
	fmt.Println("  Then apply their response: tw server join-relay --apply <response file>")
	return nil
}
