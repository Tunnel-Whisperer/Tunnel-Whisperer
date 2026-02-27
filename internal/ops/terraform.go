package ops

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// ansiRE strips ANSI escape sequences from terminal output.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// RunTerraform executes a terraform command in dir with the given env vars.
// Output is streamed line-by-line as progress events so the dashboard shows
// real-time feedback instead of blocking silently.
func (o *Ops) RunTerraform(ctx context.Context, dir string, env map[string]string, progress ProgressFunc, args ...string) error {
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
	cmd := exec.Command("terraform", "output", "-raw", name)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// TerraformAvailable returns true if terraform is on the PATH.
func TerraformAvailable() bool {
	_, err := exec.LookPath("terraform")
	return err == nil
}
