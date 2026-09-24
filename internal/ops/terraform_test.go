package ops

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tunnelwhisperer/tw/internal/config"
)

func TestTerraformEnv(t *testing.T) {
	cases := []struct {
		name  string
		uuid  string
		extra map[string]string
		id    string
	}{
		{name: "no extra", uuid: "0123abcd-4567-89ef-0123-456789abcdef", id: "0123abcd"},
		{name: "extra kept", uuid: "fedcba98-7654-3210-fedc-ba9876543210", extra: map[string]string{"HCLOUD_TOKEN": "tok"}, id: "fedcba98"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TW_CONFIG_DIR", t.TempDir())
			o := newTestOps(t)
			o.cfg.Xray.UUID = tc.uuid

			env, err := o.terraformEnv(tc.extra)
			if err != nil {
				t.Fatal(err)
			}
			dir := env["TF_DATA_DIR"]
			want, err := config.TerraformDataDir(tc.id)
			if err != nil {
				t.Fatal(err)
			}
			if dir != want {
				t.Fatalf("TF_DATA_DIR = %q, want %q", dir, want)
			}
			if !strings.HasPrefix(dir, config.CacheDir()+string(filepath.Separator)) {
				t.Errorf("TF_DATA_DIR %q not under cache dir %q", dir, config.CacheDir())
			}
			for k, v := range tc.extra {
				if env[k] != v {
					t.Errorf("env[%q] = %q, want %q", k, env[k], v)
				}
			}
			if len(env) != len(tc.extra)+1 {
				t.Errorf("env has %d keys, want %d", len(env), len(tc.extra)+1)
			}
			if _, ok := tc.extra["TF_DATA_DIR"]; ok {
				t.Fatal("extra must not be mutated")
			}
			fi, err := os.Stat(dir)
			if err != nil {
				t.Fatalf("data dir not created: %v", err)
			}
			if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
				t.Errorf("data dir mode = %v, want 0700", fi.Mode().Perm())
			}
		})
	}
}

func TestTerraformEnvRequiresUUID(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	o := newTestOps(t)
	o.cfg.Xray.UUID = ""
	if _, err := o.terraformEnv(nil); err == nil {
		t.Fatal("expected error for profile without uuid")
	}
}

func TestTerraformEnvRemovesLegacyWorkingDir(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	o := newTestOps(t)
	o.cfg.Xray.UUID = "0123abcd-4567-89ef-0123-456789abcdef"
	legacy := filepath.Join(config.RelayDir(), ".terraform")
	writeFile(t, filepath.Join(legacy, "providers", "x", "provider.bin"), "bin")
	lock := filepath.Join(config.RelayDir(), ".terraform.lock.hcl")
	writeFile(t, lock, "lock")

	if _, err := o.terraformEnv(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("legacy %s still present (err=%v)", legacy, err)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Errorf("lock file must stay in the working dir: %v", err)
	}
}

func TestTfstateOutput(t *testing.T) {
	const state = `{"version":4,"outputs":{"relay_ip":{"value":"203.0.113.7","type":"string"}}}`
	cases := []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{name: "present", output: "relay_ip", want: "203.0.113.7"},
		{name: "missing", output: "nope", wantErr: true},
	}
	path := filepath.Join(t.TempDir(), "terraform.tfstate")
	writeFile(t, path, state)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tfstateOutput(path, tc.output)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("tfstateOutput = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseSemver(t *testing.T) {
	cases := []struct {
		in      string
		want    [3]int
		wantErr bool
	}{
		{in: "1.0.0", want: [3]int{1, 0, 0}},
		{in: "1.16.1", want: [3]int{1, 16, 1}},
		{in: "0.15.5", want: [3]int{0, 15, 5}},
		{in: "1.9.0-beta1", want: [3]int{1, 9, 0}},
		{in: "1.9.0+build.7", want: [3]int{1, 9, 0}},
		{in: "v1.2.3", want: [3]int{1, 2, 3}},
		{in: "garbage", wantErr: true},
		{in: "1.2", wantErr: true},
		{in: "1.2.x", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseSemver(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("parseSemver(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestCheckTerraform(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub terraform is a shell script")
	}
	cases := []struct {
		name    string
		version string
		wantErr string
	}{
		{name: "supported", version: "1.16.1"},
		{name: "minimum", version: "1.0.0"},
		{name: "too old", version: "0.15.5", wantErr: "too old"},
		{name: "missing", wantErr: "not found in PATH"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.version != "" {
				script := "#!/bin/sh\necho '{\"terraform_version\":\"" + tc.version + "\",\"platform\":\"linux_amd64\"}'\n"
				if err := os.WriteFile(filepath.Join(dir, "terraform"), []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", dir)

			err := CheckTerraform()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckTerraform() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("CheckTerraform() = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}
