package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var serverJoinApply string

var serverJoinCmd = &cobra.Command{
	Use:   "join <relay-host>",
	Short: "Generate this server's identity and a join-request for the relay admin",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runServerJoin,
}

func init() {
	serverJoinCmd.Flags().StringVar(&serverJoinApply, "apply", "", "apply an admin join-response file instead of generating a request")
	serverCmd.AddCommand(serverJoinCmd)
}

func runServerJoin(cmd *cobra.Command, args []string) error {
	o, err := ops.New()
	if err != nil {
		return err
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
		return fmt.Errorf("relay host required: tw server join <relay-host>")
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
	fmt.Println("  Send it to the relay admin, who runs: tw admin enroll <this file>")
	fmt.Println("  Then apply their response: tw server join --apply <response file>")
	return nil
}
