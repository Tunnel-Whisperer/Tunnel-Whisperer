package cli

import (
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var setPortClear bool

var clientSetPortCmd = &cobra.Command{
	Use:   "set-port [server_port] [local_port]",
	Short: "Override the local port a tunnel binds on this machine",
	Long: `Override the local port for one tunnel, keyed by its server port.

The admin-chosen local port in the bundle is only a default — if it clashes
with something already running on this machine, remap it here. The server
port identifies the tunnel and cannot be changed from the client.

With no arguments, lists all tunnels with their default, override, and
effective local ports.

Examples:
  tw client set-port                 List tunnels and effective local ports
  tw client set-port 15432 4000      Bind server port 15432 locally on 4000
  tw client set-port 15432 --clear   Remove the override (back to the default)`,
	Args: cobra.MaximumNArgs(2),
	RunE: runClientSetPort,
}

func init() {
	clientSetPortCmd.Flags().BoolVar(&setPortClear, "clear", false, "remove the override for server_port")
	clientCmd.AddCommand(clientSetPortCmd)
}

// formatPortOverrides renders the no-arg listing: one row per tunnel with
// default, override ("-" if none), and effective local port.
func formatPortOverrides(c config.ClientConfig) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 2, 4, 3, ' ', 0)
	fmt.Fprintln(w, "  SERVER PORT\tDEFAULT\tOVERRIDE\tEFFECTIVE")
	effective := c.EffectiveTunnels(nil)
	for i, t := range c.Tunnels {
		override := "-"
		if p, ok := c.PortOverrides[t.RemotePort]; ok {
			override = strconv.Itoa(p)
		}
		fmt.Fprintf(w, "  %d\t%d\t%s\t%d\n", t.RemotePort, t.LocalPort, override, effective[i].LocalPort)
	}
	w.Flush()
	return b.String()
}

func runClientSetPort(cmd *cobra.Command, args []string) error {
	if err := requireMode("client"); err != nil {
		return err
	}

	if len(args) == 0 {
		if setPortClear {
			return fmt.Errorf("--clear needs the server port: tw client set-port <server_port> --clear")
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if len(cfg.Client.Tunnels) == 0 {
			fmt.Println("  No tunnels configured.")
			return nil
		}
		fmt.Print(formatPortOverrides(cfg.Client))
		return nil
	}

	serverPort, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid server port %q", args[0])
	}

	o, err := ops.New()
	if err != nil {
		return err
	}

	if setPortClear {
		if len(args) != 1 {
			return fmt.Errorf("--clear takes only the server port")
		}
		cleared, err := o.ClearClientPortOverride(serverPort)
		if err != nil {
			return err
		}
		if !cleared {
			fmt.Printf("  no override set for server port %d\n", serverPort)
			return nil
		}
		fmt.Printf("  Override for server port %d cleared — back to the default.\n", serverPort)
		fmt.Println("  (takes effect on next reconnect)")
		return nil
	}

	if len(args) != 2 {
		return fmt.Errorf("usage: tw client set-port <server_port> <local_port> (or --clear)")
	}
	localPort, err := strconv.Atoi(args[1])
	if err != nil {
		return fmt.Errorf("invalid local port %q", args[1])
	}
	if err := o.SetClientPortOverride(serverPort, localPort); err != nil {
		return err
	}
	fmt.Printf("  Server port %d now binds locally on %d\n", serverPort, localPort)
	fmt.Println("  (takes effect on next reconnect)")
	return nil
}
