package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/tunnelwhisperer/tw/internal/ops"
)

var relayInviteTTL time.Duration

var relayInviteCmd = &cobra.Command{
	Use:   "invite",
	Short: "Mint a one-time code that enrolls a remote server (no files)",
	Long: `Mint a one-time invite code and wait for the enrollee.

Read the code to the server operator over any channel (it cannot be abused
without also passing the SAS check below). They run:

  tw join <relay-host> <code>

When their enrollment arrives, BOTH terminals show a short authentication
string. Have the operator read theirs aloud and approve only on an exact
match. The invite is single-use and expires automatically.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireMode("relay"); err != nil {
			return err
		}
		o, err := ops.New()
		if err != nil {
			return err
		}
		resp, err := o.InviteServer(relayInviteTTL, ops.InviteUI{
			ShowCode: func(code string, expires time.Time) {
				fmt.Printf("Invite code: %s\n", code)
				fmt.Printf("Expires:     %s\n", expires.Format(time.Kitchen))
				fmt.Println("Waiting for the enrollee to run: tw join <relay-host> <code> ...")
			},
			ConfirmSAS: func(sas string) bool {
				fmt.Printf("\nSAS: %s\n", sas)
				fmt.Print("Does the enrollee read back EXACTLY this string? [y/N]: ")
				line, _ := sharedLine()
				return line == "y" || line == "Y" || line == "yes"
			},
		}, cliProgress)
		if err != nil {
			return err
		}
		fmt.Printf("Server %s enrolled (port %d).\n", resp.ServerID, resp.RemotePort)
		return nil
	},
}

func init() {
	relayInviteCmd.Flags().DurationVar(&relayInviteTTL, "ttl", 15*time.Minute, "invite lifetime")
	relayCmd.AddCommand(relayInviteCmd)
}
