package ops

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/tunnelwhisperer/tw/internal/config"
)

// ansiRE strips ANSI escape sequences from terminal output.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// terraformEnv returns extra plus TF_DATA_DIR, creating the data dir.
//
// Storage contract: relay/ holds only durable provisioning state (rendered
// config, tfvars, tfstate, lock file, metadata) and travels in the context
// bundle; everything regenerable (Terraform's .terraform provider/module data)
// lives under config.CacheDir() and is never bundled. Any future provisioner
// backend must follow the same split.
func (o *Ops) terraformEnv(extra map[string]string) (map[string]string, error) {
	uuid := o.Config().Xray.UUID
	if uuid == "" {
		return nil, errors.New("terraform data dir: profile has no uuid")
	}
	if err := removeLegacyTerraformDir(); err != nil {
		return nil, err
	}
	dir, err := config.TerraformDataDir(config.ShortID(uuid))
	if err != nil {
		return nil, fmt.Errorf("terraform data dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating terraform data dir: %w", err)
	}
	env := maps.Clone(extra)
	if env == nil {
		env = map[string]string{}
	}
	env["TF_DATA_DIR"] = dir
	return env, nil
}

// removeLegacyTerraformDir deletes relay/.terraform left by versions that ran
// Terraform without TF_DATA_DIR; init regenerates it in the cache.
func removeLegacyTerraformDir() error {
	legacy := filepath.Join(config.RelayDir(), ".terraform")
	if _, err := os.Stat(legacy); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("checking legacy terraform dir: %w", err)
	}
	if err := os.RemoveAll(legacy); err != nil {
		return fmt.Errorf("removing legacy terraform dir %s: %w", legacy, err)
	}
	slog.Info("removed legacy terraform working dir from profile", "path", legacy)
	return nil
}

// RunTerraform executes a terraform command in dir with the given env vars.
// Output is streamed line-by-line as progress events so the dashboard shows
// real-time feedback instead of blocking silently.
func (o *Ops) RunTerraform(ctx context.Context, dir string, env map[string]string, progress ProgressFunc, args ...string) error {
	env, err := o.terraformEnv(env)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "terraform", args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "TF_IN_AUTOMATION=1") // suppress color and interactive prompts
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	// Pipe stdout+stderr so we can stream to progress.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("terraform %s: stdout pipe: %w", strings.Join(args, " "), err)
	}
	cmd.Stderr = cmd.Stdout // merge stderr into stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("terraform %s: %w", strings.Join(args, " "), err)
	}

	// Stream output line-by-line, stripping any ANSI escape codes.
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)
	var lastLines []string
	for scanner.Scan() {
		line := ansiRE.ReplaceAllString(scanner.Text(), "")
		lastLines = append(lastLines, line)
		// Keep only last 50 lines for error context.
		if len(lastLines) > 50 {
			lastLines = lastLines[len(lastLines)-50:]
		}
		if progress != nil {
			progress(ProgressEvent{
				Label:   "terraform " + args[0],
				Status:  "running",
				Message: line,
			})
		}
	}

	if err := cmd.Wait(); err != nil {
		tail := strings.Join(lastLines, "\n")
		return fmt.Errorf("terraform %s: %w\n%s", strings.Join(args, " "), err, tail)
	}
	return nil
}

// TerraformOutput reads a single output value from a Terraform state.
func (o *Ops) TerraformOutput(dir string, env map[string]string, name string) (string, error) {
	env, err := o.terraformEnv(env)
	if err != nil {
		return "", err
	}
	cmd := exec.Command("terraform", "output", "-raw", name)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// tfstateOutput reads a string output value straight from a Terraform state
// file, without running terraform (and so without needing its data dir).
func tfstateOutput(path, name string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading tfstate: %w", err)
	}
	var state struct {
		Outputs map[string]struct {
			Value json.RawMessage `json:"value"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return "", fmt.Errorf("parsing tfstate %s: %w", path, err)
	}
	out, ok := state.Outputs[name]
	if !ok {
		return "", fmt.Errorf("tfstate %s: output %q not found", path, name)
	}
	var value string
	if err := json.Unmarshal(out.Value, &value); err != nil {
		return "", fmt.Errorf("tfstate %s: output %q is not a string: %w", path, name, err)
	}
	return value, nil
}

// MinTerraformVersion is the oldest Terraform the generated relay modules
// accept (their required_version = ">= 1.0").
const MinTerraformVersion = "1.0.0"

// TerraformVersion returns the version reported by `terraform version -json`.
func TerraformVersion() (string, error) {
	cmd := exec.Command("terraform", "version", "-json")
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("terraform version: %w", err)
	}
	var v struct {
		TerraformVersion string `json:"terraform_version"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", fmt.Errorf("terraform version: %w", err)
	}
	return v.TerraformVersion, nil
}

// CheckTerraform returns an actionable error unless a terraform binary of at
// least MinTerraformVersion is on the PATH.
func CheckTerraform() error {
	if _, err := exec.LookPath("terraform"); err != nil {
		return fmt.Errorf("terraform is required but not found in PATH\n  Install Terraform >= %s: https://developer.hashicorp.com/terraform/install", MinTerraformVersion)
	}
	got, err := TerraformVersion()
	if err != nil {
		return err
	}
	have, err := parseSemver(got)
	if err != nil {
		return err
	}
	minimum, err := parseSemver(MinTerraformVersion)
	if err != nil {
		return err
	}
	for i := range have {
		if have[i] != minimum[i] {
			if have[i] < minimum[i] {
				return fmt.Errorf("terraform %s is too old — tw needs >= %s\n  Upgrade: https://developer.hashicorp.com/terraform/install", got, MinTerraformVersion)
			}
			break
		}
	}
	return nil
}

// parseSemver parses MAJOR.MINOR.PATCH with an optional leading "v"; any
// -prerelease or +build suffix is ignored.
func parseSemver(s string) ([3]int, error) {
	var v [3]int
	core := strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("unrecognised terraform version %q", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, fmt.Errorf("unrecognised terraform version %q", s)
		}
		v[i] = n
	}
	return v, nil
}
