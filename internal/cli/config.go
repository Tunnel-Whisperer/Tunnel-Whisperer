package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/ops"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage relay contexts (switch between relays/identities)",
}

var configGetContextsCmd = &cobra.Command{
	Use:   "get-contexts",
	Short: "List stored contexts",
	RunE:  runConfigGetContexts,
}

var configCurrentContextCmd = &cobra.Command{
	Use:   "current-context",
	Short: "Print the active context",
	RunE:  runConfigCurrentContext,
}

var configUseContextCmd = &cobra.Command{
	Use:   "use-context <name>",
	Short: "Switch the active context (re-seals current, reconnects)",
	Args:  cobra.ExactArgs(1),
	RunE:  runConfigUseContext,
}

var configRenameContextCmd = &cobra.Command{
	Use:   "rename-context <old> <new>",
	Short: "Rename a context",
	Args:  cobra.ExactArgs(2),
	RunE:  runConfigRenameContext,
}

var configDeleteContextCmd = &cobra.Command{
	Use:   "delete-context <name>",
	Short: "Delete a stored context",
	Args:  cobra.ExactArgs(1),
	RunE:  runConfigDeleteContext,
}

var configImportCmd = &cobra.Command{
	Use:   "import <bundle.zip>",
	Short: "Import a bundle as a new context",
	Args:  cobra.ExactArgs(1),
	RunE:  runConfigImport,
}

var configImportName string

var configExportCmd = &cobra.Command{
	Use:   "export [name]",
	Short: "Export a context as a portable bundle",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runConfigExport,
}

func init() {
	configImportCmd.Flags().StringVar(&configImportName, "name", "", "context name (default: relay domain)")
	configCmd.AddCommand(configGetContextsCmd, configCurrentContextCmd, configUseContextCmd,
		configRenameContextCmd, configDeleteContextCmd, configImportCmd, configExportCmd)
	rootCmd.AddCommand(configCmd)
}

func runConfigGetContexts(cmd *cobra.Command, args []string) error {
	o, err := ops.New()
	if err != nil {
		return err
	}
	list, err := o.ListContexts()
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "CURRENT\tNAME\tROLE\tRELAY")
	for _, c := range list {
		cur := ""
		if c.Current {
			cur = "*"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", cur, c.Name, c.Role, c.Relay)
	}
	return w.Flush()
}

func runConfigCurrentContext(cmd *cobra.Command, args []string) error {
	o, err := ops.New()
	if err != nil {
		return err
	}
	cur, err := o.CurrentContext()
	if err != nil {
		return err
	}
	if cur == "" {
		return fmt.Errorf("no current context")
	}
	fmt.Println(cur)
	return nil
}

func runConfigUseContext(cmd *cobra.Command, args []string) error {
	o, err := ops.New()
	if err != nil {
		return err
	}
	pass, err := readSecret(fmt.Sprintf("Passphrase for context %q", args[0]))
	if err != nil {
		return err
	}
	// currentPassphrase is prompted lazily by ops only if needed; pass "" and
	// let UseContext error with guidance if it must re-seal a changed current
	// context that has no cached passphrase.
	if err := o.UseContext(args[0], pass, "", cliProgress); err != nil {
		return err
	}
	fmt.Printf("  Switched to context %q.\n", args[0])
	return nil
}

func runConfigRenameContext(cmd *cobra.Command, args []string) error {
	o, err := ops.New()
	if err != nil {
		return err
	}
	if err := o.RenameContext(args[0], args[1]); err != nil {
		return err
	}
	fmt.Printf("  Renamed %q to %q.\n", args[0], args[1])
	return nil
}

func runConfigDeleteContext(cmd *cobra.Command, args []string) error {
	o, err := ops.New()
	if err != nil {
		return err
	}
	if err := o.DeleteContext(args[0]); err != nil {
		return err
	}
	fmt.Printf("  Deleted context %q.\n", args[0])
	return nil
}

func runConfigImport(cmd *cobra.Command, args []string) error {
	data, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("reading bundle: %w", err)
	}
	pass, err := readSecret("Bundle passphrase")
	if err != nil {
		return err
	}
	o, err := ops.New()
	if err != nil {
		return err
	}
	name := configImportName
	if name == "" {
		return fmt.Errorf("a context name is required: pass --name")
	}
	if err := o.ImportContext(data, name, pass); err != nil {
		return err
	}
	fmt.Printf("  Imported context %q. Switch with: tw config use-context %s\n", name, name)
	return nil
}

func runConfigExport(cmd *cobra.Command, args []string) error {
	o, err := ops.New()
	if err != nil {
		return err
	}
	name := ""
	if len(args) == 1 {
		name = args[0]
	} else {
		if name, err = o.CurrentContext(); err != nil {
			return err
		}
	}
	data, err := o.ExportContext(name)
	if err != nil {
		return err
	}
	fname := fmt.Sprintf("tw_%s.twctx", name)
	if err := os.WriteFile(fname, data, 0600); err != nil {
		return fmt.Errorf("writing %s: %w", fname, err)
	}
	fmt.Printf("  Exported context %q to %s\n", name, fname)
	return nil
}
