package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/tunnelwhisperer/tw/internal/api"
	"github.com/tunnelwhisperer/tw/internal/config"
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

var configNewContextCmd = &cobra.Command{
	Use:   "new-context <name>",
	Short: "Create a fresh empty context and switch to it (the current one is preserved)",
	Args:  cobra.ExactArgs(1),
	RunE:  runConfigNewContext,
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

var (
	configImportName     string
	configImportActivate bool
	configImportForce    bool
)

var configExportCmd = &cobra.Command{
	Use:   "export [name]",
	Short: "Export a context as a portable bundle",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runConfigExport,
}

func init() {
	configImportCmd.Flags().StringVar(&configImportName, "name", "", "context name (default: relay domain)")
	configImportCmd.Flags().BoolVar(&configImportActivate, "activate", false, "switch to the imported context immediately (applies its mode)")
	configImportCmd.Flags().BoolVar(&configImportForce, "force", false, "replace an existing context of the same name without prompting")
	configCmd.AddCommand(configGetContextsCmd, configCurrentContextCmd, configUseContextCmd,
		configNewContextCmd, configRenameContextCmd, configDeleteContextCmd, configImportCmd, configExportCmd)
	rootCmd.AddCommand(configCmd)
}

// newContextSealingCurrent creates a fresh empty context named `name` and
// switches to it, prompting for a passphrase to seal (and preserve) the current
// context first. Shared by `tw config new-context` and `tw server join --new-context`.
func newContextSealingCurrent(o *ops.Ops, name string) error {
	cur, _ := o.CurrentContext()
	if cur != "" {
		fmt.Printf("  Set a passphrase to seal the current context %q (you'll need it to switch back):\n", cur)
		pass, err := promptNewPassphrase()
		if err != nil {
			return err
		}
		return o.NewContext(name, pass)
	}
	return o.NewContext(name, "")
}

func runConfigNewContext(cmd *cobra.Command, args []string) error {
	o, err := ops.New()
	if err != nil {
		return err
	}
	if err := newContextSealingCurrent(o, args[0]); err != nil {
		return err
	}
	fmt.Printf("  Created context %q and switched to it. Configure it (e.g. tw server join / a relay), then\n", args[0])
	fmt.Println("  switch back any time with: tw config use-context <name>")
	return nil
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
	// Try with no current-context passphrase first (the daemon may have it
	// cached, or the current context may be empty). If sealing the current
	// context needs a passphrase, prompt to set/enter it and retry — so a
	// configured context is never silently dropped on switch.
	err = o.UseContext(args[0], pass, "", cliProgress)
	if errors.Is(err, ops.ErrCurrentNeedsPassphrase) {
		cur, _ := o.CurrentContext()
		fmt.Printf("  Set a passphrase to seal the current context %q (you'll need it to switch back):\n", cur)
		curPass, perr := promptNewPassphrase()
		if perr != nil {
			return perr
		}
		err = o.UseContext(args[0], pass, curPass, cliProgress)
	}
	if err != nil {
		return err
	}
	fmt.Printf("  Switched to context %q.\n", args[0])
	warnIfDaemonStale()
	return nil
}

// warnIfDaemonStale prints the daemon/context mismatch warning after a switch
// if a tw service is running and still serving its old config. Best-effort: no
// daemon, or a daemon already in sync, prints nothing.
func warnIfDaemonStale() {
	cfg, err := config.Load()
	if err != nil {
		return
	}
	c, err := api.Dial(fmt.Sprintf("localhost:%d", cfg.Server.APIPort))
	if err != nil {
		return
	}
	defer c.Close()
	resp, err := c.GetStatus(context.Background())
	if err != nil {
		return
	}
	if w := daemonContextMismatch(resp.Mode, resp.Relay.Domain); w != "" {
		fmt.Println(w)
	}
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
	name := args[0]
	cur, _ := o.CurrentContext()
	list, _ := o.ListContexts()
	resetting := name == cur && len(list) == 1
	if resetting {
		// A running daemon holds files in the config dir open; on Windows the
		// wipe would only partially succeed and orphan the config. Refuse before
		// deleting anything so the reset is all-or-nothing.
		cfg, _ := config.Load()
		if c, derr := api.Dial(fmt.Sprintf("localhost:%d", cfg.Server.APIPort)); derr == nil {
			c.Close()
			return fmt.Errorf("the tw service is running and holds the config folder open; stop it first (Windows: Stop-Service tw; otherwise: tw service stop), then run this again")
		}
		fmt.Printf("  %q is the only context and is active. Deleting it performs a FULL RESET:\n", name)
		fmt.Println("  it removes all tw configuration (identity, keys, relay data) from this machine.")
		fmt.Print("  Continue? [y/N]: ")
		if ans, _ := sharedLine(); strings.ToLower(ans) != "y" {
			fmt.Println("  Aborted.")
			return nil
		}
	}
	if err := o.DeleteContext(name); err != nil {
		return err
	}
	if resetting {
		fmt.Println("  Configuration removed. This machine is no longer set up.")
	} else {
		fmt.Printf("  Deleted context %q.\n", name)
	}
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
	name, err := o.ImportContext(data, configImportName, pass, configImportForce)
	if errors.Is(err, ops.ErrContextExists) {
		// Don't rewrite an existing context unasked. Keep it by default; only
		// replace on explicit confirmation (or --force).
		fmt.Printf("  Context %q already exists. Replace it with this bundle? [y/N]: ", name)
		if ans, _ := sharedLine(); strings.ToLower(ans) != "y" {
			fmt.Println("  Kept the existing context; nothing changed.")
			return nil
		}
		name, err = o.ImportContext(data, configImportName, pass, true)
	}
	if err != nil {
		return err
	}
	if configImportActivate {
		if aerr := activateImported(o, name, pass); aerr != nil {
			return aerr
		}
		fmt.Printf("  Imported and switched to context %q (mode applied from the bundle).\n", name)
		return nil
	}
	fmt.Printf("  Imported context %q. Activate with: tw config use-context %s\n", name, name)
	return nil
}

// activateImported switches to a freshly imported context, prompting for the
// current context's passphrase if it must be re-sealed first (same retry the
// use-context command does).
func activateImported(o *ops.Ops, name, pass string) error {
	err := o.UseContext(name, pass, "", cliProgress)
	if errors.Is(err, ops.ErrCurrentNeedsPassphrase) {
		cur, _ := o.CurrentContext()
		fmt.Printf("  Set a passphrase to seal the current context %q (you'll need it to switch back):\n", cur)
		curPass, perr := promptNewPassphrase()
		if perr != nil {
			return perr
		}
		err = o.UseContext(name, pass, curPass, cliProgress)
	}
	return err
}

func runConfigExport(cmd *cobra.Command, args []string) error {
	o, err := ops.New()
	if err != nil {
		return err
	}
	cur, err := o.CurrentContext()
	if err != nil {
		return err
	}
	name := cur
	if len(args) == 1 {
		name = args[0]
	}
	// The active context has no on-disk snapshot until a switch seals it, so
	// export it by sealing the live profile; a non-current context is already
	// sealed on disk.
	if name == cur || name == "" {
		return writeProfileBundle(o, name)
	}
	data, err := o.ExportContext(name)
	if err != nil {
		return err
	}
	return writeBundleFile(name, data)
}

// writeProfileBundle seals the active profile under a freshly-prompted passphrase
// and writes it as tw_<name>.twctx. This is the single portable bundle format
// (admin/server/client alike) — it supersedes the old admin bundle.
func writeProfileBundle(o *ops.Ops, name string) error {
	pass, err := promptNewPassphrase()
	if err != nil {
		return err
	}
	data, err := o.ExportCurrentContext(pass)
	if err != nil {
		return err
	}
	return writeBundleFile(name, data)
}

func writeBundleFile(name string, data []byte) error {
	safe := strings.NewReplacer(".", "-", ":", "-", "/", "-", " ", "-").Replace(name)
	if safe == "" {
		safe = "context"
	}
	fname := fmt.Sprintf("tw_%s.twctx", safe)
	if err := os.WriteFile(fname, data, 0600); err != nil {
		return fmt.Errorf("writing %s: %w", fname, err)
	}
	abs, err := filepath.Abs(fname)
	if err != nil {
		abs = fname
	}
	fmt.Printf("\n  Bundle written: %s\n", abs)
	fmt.Println("  IMPORTANT: back this up securely and remember its passphrase. It is the")
	fmt.Println("  portable identity for this relay/context; there is no recovery if it is lost.")
	return nil
}
