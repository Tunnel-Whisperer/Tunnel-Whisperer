package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var relayEnrollServerCmd = &cobra.Command{
	Use:   "enroll-server <join-request.json>",
	Short: "Enroll a joining server onto the relay and write a join-response",
	Args:  cobra.ExactArgs(1),
	RunE:  runRelayEnrollServer,
}

var relayGetServersCmd = &cobra.Command{
	Use:   "get-servers",
	Short: "List servers registered on the relay",
	RunE:  runRelayGetServers,
}

func init() {
	relayCmd.AddCommand(relayEnrollServerCmd)
	relayCmd.AddCommand(relayGetServersCmd)
}

func runRelayEnrollServer(cmd *cobra.Command, args []string) error {
	if err := requireMode("admin"); err != nil {
		return err
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("reading join request: %w", err)
	}
	req, err := ops.DecodeJoinRequest(data)
	if err != nil {
		return err
	}
	o, err := ops.New()
	if err != nil {
		return err
	}
	resp, err := o.EnrollServer(req, cliProgress)
	if err != nil {
		return err
	}
	out, err := resp.Encode()
	if err != nil {
		return err
	}
	fname := fmt.Sprintf("tw_join_response_%s.json", resp.ServerID)
	if err := os.WriteFile(fname, out, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", fname, err)
	}
	fmt.Printf("\n  Enrolled %s on port %d. Send the response back: %s\n", resp.ServerID, resp.RemotePort, fname)
	return nil
}

func runRelayGetServers(cmd *cobra.Command, args []string) error {
	if err := requireMode("admin"); err != nil {
		return err
	}
	o, err := ops.New()
	if err != nil {
		return err
	}
	list, err := o.ListServers()
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "SERVER-ID\tHOSTNAME\tREMOTE-PORT")
	for _, s := range list {
		fmt.Fprintf(w, "%s\t%s\t%d\n", s.ServerID, s.Hostname, s.RemotePort)
	}
	return w.Flush()
}
