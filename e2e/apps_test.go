//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// testAppCatalog drives the application-template catalog end to end on the
// server. Templates are a server-local library of named port-mapping sets —
// they never touch the relay or any user (editing one explicitly does not
// affect users, per the CLI's own note), so every step here is local CRUD
// through the interactive wizards, driven by piped stdin.
func testAppCatalog(t *testing.T) {
	scenario(t, "application templates are a server-local catalog: create, list, edit (rename + remap), delete",
		"tw server app create (piped wizard: name + one mapping) creates the template",
		"tw server app list shows the template with its mapping",
		"tab completion: tw __complete server app edit offers the template",
		"tw server app edit renames and remaps it, and prints the does-not-affect-users note",
		"tw server app edit on an unknown name fails with 'not found'",
		"tw server app delete aborts on 'n' and deletes on 'y'",
		"the client-role gate refuses tw server app list (requires server mode)")

	// Re-runnability: wipe any templates a prior interrupted run left behind.
	for _, name := range []string{"e2eapp", "e2eapp2"} {
		if out, err := execInOK("server", "printf 'y\\n' | tw server app delete "+name); err == nil {
			t.Logf("pre-cleanup: deleted leftover template %q:\n%s", name, out)
		}
	}

	// Create: wizard reads name, then mappings until an empty client port.
	out := execIn(t, "server", `printf 'e2eapp\n8080\n80\n\n' | tw server app create`)
	if !strings.Contains(out, `Application "e2eapp" created.`) {
		fatalf(t, "app create did not confirm:\n%s", out)
	}

	out = execIn(t, "server", "tw server app list")
	if !strings.Contains(out, "e2eapp (1 mapping)") || !strings.Contains(out, "8080 → 80") {
		fatalf(t, "app list missing e2eapp or its mapping:\n%s", out)
	}

	// Tab completion offers the template for app-selecting commands.
	compOut := execIn(t, "server", `tw __complete server app edit ""`)
	if !strings.Contains(compOut, "e2eapp") {
		fatalf(t, "app edit completion does not offer e2eapp:\n%s", compOut)
	}

	// Edit: rename to e2eapp2 and replace the mapping. The wizard must show
	// the current mappings first and end with the does-not-affect-users note.
	out = execIn(t, "server", `printf 'e2eapp2\n9090\n90\n\n' | tw server app edit e2eapp`)
	if !strings.Contains(out, "8080 → 80") {
		fatalf(t, "app edit did not show the current mapping before prompting:\n%s", out)
	}
	if !strings.Contains(out, `Application "e2eapp2" updated.`) ||
		!strings.Contains(out, "does not affect users") {
		fatalf(t, "app edit did not confirm the rename or print the users note:\n%s", out)
	}
	out = execIn(t, "server", "tw server app list")
	if !strings.Contains(out, "e2eapp2 (1 mapping)") || !strings.Contains(out, "9090 → 90") {
		fatalf(t, "app list missing renamed e2eapp2 or its new mapping:\n%s", out)
	}
	if strings.Contains(out, "8080") {
		fatalf(t, "app list still shows the pre-edit mapping:\n%s", out)
	}

	// Editing an unknown template fails before any prompt.
	if out, err := execInOK("server", "tw server app edit nosuchapp < /dev/null"); err == nil {
		fatalf(t, "app edit of an unknown template unexpectedly succeeded:\n%s", out)
	} else if !strings.Contains(out, "not found") {
		fatalf(t, "app edit of an unknown template: expected 'not found', got:\n%s", out)
	}

	// Delete prompts [y/N]: 'n' aborts and keeps the template, 'y' removes it.
	out = execIn(t, "server", `printf 'n\n' | tw server app delete e2eapp2`)
	if !strings.Contains(out, "Aborted.") {
		fatalf(t, "app delete with 'n' did not abort:\n%s", out)
	}
	if out = execIn(t, "server", "tw server app list"); !strings.Contains(out, "e2eapp2") {
		fatalf(t, "aborted delete removed the template anyway:\n%s", out)
	}
	out = execIn(t, "server", `printf 'y\n' | tw server app delete e2eapp2`)
	if !strings.Contains(out, `Application "e2eapp2" deleted.`) {
		fatalf(t, "app delete did not confirm:\n%s", out)
	}
	if out = execIn(t, "server", "tw server app list"); strings.Contains(out, "e2eapp") {
		fatalf(t, "deleted template still listed:\n%s", out)
	}

	// The client-role gate refuses app commands (requireMode("server")).
	if gateOut, gateErr := execInOK("client", "tw server app list"); gateErr == nil {
		fatalf(t, "client profile was allowed to run tw server app list:\n%s", gateOut)
	} else if !strings.Contains(gateOut, "requires server mode") {
		fatalf(t, "expected a server-mode gate error, got:\n%s", gateOut)
	}
}
