package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var relayAddServerSwitch bool

var relayAddServerCmd = &cobra.Command{
	Use:   "add-server [<context-name>]",
	Short: "Enroll this machine as a server on the relay, stored as a new context",
	Long: `Enroll this machine as a server tenant on its own relay, in one command.

This is the single-operator shortcut for running relay and server on the same
machine: the whole join handshake happens in-process — no join-request or
join-response files, no context switching mid-flow. The server identity is
generated fresh, enrolled on the relay, signed by the relay's key, and stored
as a new ready-to-use context. The active relay context is not modified.

The default context name is server-<relay's first DNS label>.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runRelayAddServer,
}

func init() {
	relayAddServerCmd.Flags().BoolVar(&relayAddServerSwitch, "switch", false, "switch to the new server context after creating it")
	relayCmd.AddCommand(relayAddServerCmd)
}

func runRelayAddServer(cmd *cobra.Command, args []string) error {
	if err := requireMode("relay"); err != nil {
		return err
	}
	if err := requireWritableConfig(); err != nil {
		return err
	}
	name := ""
	if len(args) == 1 {
		name = args[0]
	}
	o, err := ops.New()
	if err != nil {
		return err
	}
	res, err := o.AddLocalServer(name, cliProgress)
	if err != nil {
		return err
	}
	fmt.Printf("\n  Server context %q created and enrolled (server-id %s, port %d).\n", res.ContextName, res.ServerID, res.RemotePort)
	if def := config.Default().Server; res.DashboardPort != def.DashboardPort || res.APIPort != def.APIPort {
		fmt.Printf("  Daemon ports adjusted to coexist with the relay context: dashboard :%d, API :%d.\n", res.DashboardPort, res.APIPort)
	}
	if relayAddServerSwitch {
		if err := o.UseContext(res.ContextName, cliProgress); err != nil {
			return err
		}
		fmt.Printf("  Switched to context %q.\n", res.ContextName)
		warnIfDaemonStale()
		fmt.Println("  Next: tw server start")
		return nil
	}
	fmt.Printf("  Next: tw config use-context %s && tw server start\n", res.ContextName)
	return nil
}
