package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var unenrollYes bool

var relayUnenrollServerCmd = &cobra.Command{
	Use:   "un-enroll-server <server-id>",
	Short: "Un-enroll a server from the relay and kill its live connections",
	Args:  cobra.ExactArgs(1),
	RunE:  runRelayUnenrollServer,
}

func init() {
	relayUnenrollServerCmd.Flags().BoolVar(&unenrollYes, "yes", false, "skip the confirmation prompt")
	relayCmd.AddCommand(relayUnenrollServerCmd)
}

func runRelayUnenrollServer(cmd *cobra.Command, args []string) error {
	if err := requireMode("admin"); err != nil {
		return err
	}
	serverID := args[0]
	o, err := ops.New()
	if err != nil {
		return err
	}
	servers, err := o.ListServers()
	if err != nil {
		return err
	}
	var target *ops.RegisteredServer
	for i := range servers {
		if servers[i].ServerID == serverID {
			target = &servers[i]
		}
	}
	if target == nil {
		return fmt.Errorf("server %q is not enrolled", serverID)
	}

	if !unenrollYes {
		enrolled := "-"
		if t, err := time.Parse(time.RFC3339, target.EnrolledAt); err == nil {
			enrolled = t.Format("2006-01-02T15:04")
		}
		fmt.Printf("\n  Server:   %s\n  Port:     %d\n  Enrolled: %s\n\n", target.ServerID, target.RemotePort, enrolled)
		fmt.Print("  Un-enroll this server? Its relay access and all its live connections end immediately. [y/N]: ")
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		if answer := strings.TrimSpace(strings.ToLower(scanner.Text())); answer != "y" {
			fmt.Println("  Aborted.")
			return nil
		}
		fmt.Println()
	}

	if err := o.UnenrollServer(serverID, cliProgress); err != nil {
		return err
	}
	fmt.Printf("\n  Un-enrolled %s. Run 'tw server join-relay' on it to re-enroll.\n", serverID)
	return nil
}
