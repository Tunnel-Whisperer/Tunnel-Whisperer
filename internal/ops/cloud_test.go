package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// clearAWSAmbient removes every ambient AWS credential source the probe looks
// at: env vars and the shared files under $HOME/.aws.
func clearAWSAmbient(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_PROFILE",
		"AWS_SHARED_CREDENTIALS_FILE", "AWS_CONFIG_FILE",
		"AWS_ROLE_ARN", "AWS_WEB_IDENTITY_TOKEN_FILE",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI",
	} {
		t.Setenv(v, "")
	}
	t.Setenv("HOME", t.TempDir())
}

func TestAWSCredsEnv(t *testing.T) {
	env, err := AWSCredsEnv("AKIAEXAMPLEEXAMPLE", "secret")
	if err != nil {
		t.Fatalf("both provided: unexpected error: %v", err)
	}
	if env["AWS_ACCESS_KEY_ID"] != "AKIAEXAMPLEEXAMPLE" || env["AWS_SECRET_ACCESS_KEY"] != "secret" {
		t.Fatalf("both provided: wrong env map: %v", env)
	}

	env, err = AWSCredsEnv("", "")
	if err != nil {
		t.Fatalf("both blank: unexpected error: %v", err)
	}
	if env != nil {
		t.Fatalf("both blank: want nil env (inherit ambient), got %v", env)
	}

	if _, err := AWSCredsEnv("AKIAEXAMPLEEXAMPLE", ""); err == nil {
		t.Fatal("key without secret: want error, got nil")
	}
	if _, err := AWSCredsEnv("", "secret"); err == nil {
		t.Fatal("secret without key: want error, got nil")
	}
}

func TestTestCloudCredentialsAWSHalfFilled(t *testing.T) {
	o := &Ops{}
	if err := o.TestCloudCredentials("AWS", "AKIAEXAMPLEEXAMPLE", ""); err == nil {
		t.Fatal("half-filled pair: want error, got nil")
	}
}

func TestTestCloudCredentialsAWSBlankNoAmbient(t *testing.T) {
	clearAWSAmbient(t)
	o := &Ops{}
	err := o.TestCloudCredentials("AWS", "", "")
	if err == nil {
		t.Fatal("blank creds with no ambient source: want error, got nil")
	}
	if !strings.Contains(err.Error(), "AWS") {
		t.Fatalf("error should mention AWS credential sources, got: %v", err)
	}
}

func TestTestCloudCredentialsAWSBlankAmbientSources(t *testing.T) {
	o := &Ops{}

	t.Run("env keys", func(t *testing.T) {
		clearAWSAmbient(t)
		t.Setenv("AWS_ACCESS_KEY_ID", "AKIAEXAMPLEEXAMPLE")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
		if err := o.TestCloudCredentials("AWS", "", ""); err != nil {
			t.Fatalf("env keys present: unexpected error: %v", err)
		}
	})

	t.Run("profile", func(t *testing.T) {
		clearAWSAmbient(t)
		t.Setenv("AWS_PROFILE", "myprofile")
		if err := o.TestCloudCredentials("AWS", "", ""); err != nil {
			t.Fatalf("AWS_PROFILE set: unexpected error: %v", err)
		}
	})

	t.Run("shared credentials file", func(t *testing.T) {
		clearAWSAmbient(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		credFile := filepath.Join(home, ".aws", "credentials")
		if err := os.MkdirAll(filepath.Dir(credFile), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(credFile, []byte("[default]\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := o.TestCloudCredentials("AWS", "", ""); err != nil {
			t.Fatalf("~/.aws/credentials present: unexpected error: %v", err)
		}
	})

	t.Run("explicit shared file env", func(t *testing.T) {
		clearAWSAmbient(t)
		f := filepath.Join(t.TempDir(), "creds")
		if err := os.WriteFile(f, []byte("[default]\n"), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AWS_SHARED_CREDENTIALS_FILE", f)
		if err := o.TestCloudCredentials("AWS", "", ""); err != nil {
			t.Fatalf("AWS_SHARED_CREDENTIALS_FILE present: unexpected error: %v", err)
		}
	})
}

func TestTestCloudCredentialsAWSExplicitStillValidated(t *testing.T) {
	o := &Ops{}
	if err := o.TestCloudCredentials("AWS", "short", "longenoughsecretlongenoughsecret"); err == nil {
		t.Fatal("short Access Key ID: want error, got nil")
	}
	if err := o.TestCloudCredentials("AWS", "AKIAEXAMPLEEXAMPLE", "short"); err == nil {
		t.Fatal("short Secret Access Key: want error, got nil")
	}
}

func TestAWSProvisionRegion(t *testing.T) {
	// Explicit keys keep the historical us-east-1 default: before ambient
	// mode existed, keys-only provisioning always landed there.
	if got := awsProvisionRegion(true, ""); got != "us-east-1" {
		t.Errorf("explicit keys, no choice: want us-east-1, got %q", got)
	}
	if got := awsProvisionRegion(true, "eu-central-1"); got != "eu-central-1" {
		t.Errorf("explicit keys, chosen region: want eu-central-1, got %q", got)
	}
	// Ambient credentials inherit the ambient region unless one was chosen.
	if got := awsProvisionRegion(false, ""); got != "" {
		t.Errorf("ambient creds, no choice: want empty (inherit), got %q", got)
	}
	if got := awsProvisionRegion(false, "eu-central-1"); got != "eu-central-1" {
		t.Errorf("ambient creds, chosen region: want eu-central-1, got %q", got)
	}
}
