package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var joinName string

var joinCmd = &cobra.Command{
	Use:   "join <relay-host> <code>",
	Short: "Join a relay with a spoken invite code (no files)",
	Long: `Join a relay using a one-time invite code from its operator.

The issuer decides the role (server tenant or client user). When prompted,
READ THE DISPLAYED STRING ALOUD to the issuer — they approve only on an
exact match. The result is stored as a new context; on a fresh machine it
is activated immediately. The context name is auto-derived from the relay
host (server role) or the granted username (client role) unless --name is
given.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireWritableConfig(); err != nil {
			return err
		}
		o, err := ops.New() // same constructor as relay_addserver.go:42
		if err != nil {
			return err
		}
		res, err := o.Join(cmd.Context(), args[0], args[1], joinName, ops.JoinUI{
			ShowSAS: func(sas string) {
				fmt.Printf("\nSAS: %s\n", sas)
				fmt.Println("Read this string to the issuer now; waiting for their approval ...")
			},
			ResolvePort: promptResolvePort,
		}, cliProgress)
		if err != nil {
			return err
		}
		fmt.Printf("Context %q (%s) created.\n", res.ContextName, res.Role)
		if res.Switched {
			fmt.Printf("Next: tw %s start\n", map[string]string{"server": "server", "client": "client"}[res.Role])
		} else {
			fmt.Printf("Next: tw config use-context %s\n", res.ContextName)
		}
		return nil
	},
}

// promptResolvePort asks for a replacement local port when the granted one is
// busy on this machine (client role).
func promptResolvePort(t config.Tunnel) int {
	fmt.Printf("Local port %d (→ %d) is in use. Enter a different local port: ", t.LocalPort, t.RemotePort)
	for {
		line, ok := sharedLine()
		if !ok {
			return 0
		}
		var p int
		if _, err := fmt.Sscanf(line, "%d", &p); err == nil && p > 0 && p < 65536 {
			return p
		}
		fmt.Print("Enter a port number 1-65535: ")
	}
}

func init() {
	joinCmd.Flags().StringVar(&joinName, "name", "", "context name (default: auto-derived from relay host / username)")
	rootCmd.AddCommand(joinCmd)
}
