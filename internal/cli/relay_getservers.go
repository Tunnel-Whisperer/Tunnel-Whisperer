package cli

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var relayGetServersCmd = &cobra.Command{
	Use:   "get-servers",
	Short: "List servers registered on the relay",
	RunE:  runRelayGetServers,
}

func init() {
	relayCmd.AddCommand(relayGetServersCmd)
}

func runRelayGetServers(cmd *cobra.Command, args []string) error {
	if err := requireMode("relay"); err != nil {
		return err
	}
	o, err := ops.New()
	if err != nil {
		return err
	}
	list, err := o.GetServerDetails()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("No servers enrolled.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "SERVER-ID\tPATH\tPORT\tENROLLED\tTUNNEL")
	for _, s := range list {
		enrolled := "-"
		if t, err := time.Parse(time.RFC3339, s.EnrolledAt); err == nil {
			enrolled = t.Format("2006-01-02T15:04")
		}
		tunnel := "down"
		if s.TunnelUp {
			tunnel = "up"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", s.ServerID, s.Path, s.RemotePort, enrolled, tunnel)
	}
	return w.Flush()
}
