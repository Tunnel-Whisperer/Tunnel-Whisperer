package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// TestEveryCommandHasE2ECoverage walks the real Cobra tree and requires every
// runnable command to be mapped in e2e/coverage.yaml — either to at least one
// e2e test name or to an explicit exemption with a reason. This is the
// mechanical guarantee that the e2e suite cannot silently go stale: adding a
// CLI surface without declaring its coverage fails the ordinary build.
func TestEveryCommandHasE2ECoverage(t *testing.T) {
	data, err := os.ReadFile("../../e2e/coverage.yaml")
	if err != nil {
		t.Fatalf("reading e2e/coverage.yaml: %v (every runnable command needs an entry there)", err)
	}
	var doc struct {
		Commands map[string]yaml.Node `yaml:"commands"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing e2e/coverage.yaml: %v", err)
	}
	if doc.Commands == nil {
		t.Fatal("e2e/coverage.yaml: missing top-level 'commands' map")
	}

	for path, node := range doc.Commands {
		if err := validateEntry(node); err != nil {
			t.Errorf("coverage.yaml entry %q: %v", path, err)
		}
	}

	var leaves []string
	var walk func(c *cobra.Command, prefix []string)
	walk = func(c *cobra.Command, prefix []string) {
		for _, sub := range c.Commands() {
			if sub.Name() == "help" || sub.Hidden {
				continue
			}
			p := append(append([]string{}, prefix...), sub.Name())
			if sub.Runnable() {
				leaves = append(leaves, strings.Join(p, " "))
			}
			walk(sub, p)
		}
	}
	walk(rootCmd, nil)
	sort.Strings(leaves)

	leafSet := map[string]bool{}
	var missing []string
	for _, l := range leaves {
		leafSet[l] = true
		if _, ok := doc.Commands[l]; !ok {
			missing = append(missing, l)
		}
	}
	var stale []string
	for path := range doc.Commands {
		if !leafSet[path] {
			stale = append(stale, path)
		}
	}
	sort.Strings(stale)

	if len(missing) > 0 {
		t.Errorf("commands with no e2e coverage entry (add them to e2e/coverage.yaml,\n"+
			"mapped to a TestE2E scenario or {exempt: \"reason\"}):\n  %s",
			strings.Join(missing, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("stale e2e/coverage.yaml entries (command no longer exists — remove or rename):\n  %s",
			strings.Join(stale, "\n  "))
	}
}

// validateEntry accepts a non-empty string list (test names) or a mapping
// containing a non-empty "exempt" reason.
func validateEntry(n yaml.Node) error {
	switch n.Kind {
	case yaml.SequenceNode:
		var tests []string
		if err := n.Decode(&tests); err != nil {
			return fmt.Errorf("want a list of test names: %w", err)
		}
		if len(tests) == 0 {
			return fmt.Errorf("empty test list")
		}
		for _, s := range tests {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("blank test name")
			}
		}
		return nil
	case yaml.MappingNode:
		var m struct {
			Exempt string `yaml:"exempt"`
		}
		if err := n.Decode(&m); err != nil {
			return fmt.Errorf("want {exempt: \"reason\"}: %w", err)
		}
		if strings.TrimSpace(m.Exempt) == "" {
			return fmt.Errorf("exempt entry needs a non-empty reason")
		}
		return nil
	default:
		return fmt.Errorf("want a list of test names or {exempt: \"reason\"}")
	}
}
