package cli

import (
	"os"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion",
	Short: "Generate zsh completion script",
	Long: `Generate zsh completion script for tw.

To load completions in your current shell session:

  source <(tw completion)

To load completions for every new session, add to your ~/.zshrc:

  source <(tw completion)

Or write to the zsh completions directory:

  tw completion > "${fpath[1]}/_tw"`,
	Args:      cobra.NoArgs,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return rootCmd.GenZshCompletion(os.Stdout)
	},
}

func init() {
	rootCmd.AddCommand(completionCmd)
}
