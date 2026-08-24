package ops

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// CloudRegion is a selectable region/location for a cloud provider.
type CloudRegion struct {
	Key  string `json:"key"`  // terraform value (e.g. "us-east-1")
	Name string `json:"name"` // display label
}

// CloudProvider describes one supported cloud provider.
type CloudProvider struct {
	Name      string        `json:"name"`
	Key       string        `json:"key"`        // matches terraform.Config.Provider
	TokenName string        `json:"token_name"` // display label for the credential
	TokenLink string        `json:"token_link"` // URL where the user creates the token
	VarName   string        `json:"var_name"`   // Terraform variable name (empty for AWS)
	Regions   []CloudRegion `json:"regions"`    // available regions
}

// CloudProviders returns the list of supported cloud providers.
func CloudProviders() []CloudProvider {
	return []CloudProvider{
		{
			Name:      "Hetzner",
			Key:       "hetzner",
			TokenName: "API Token",
			TokenLink: "https://console.hetzner.cloud → Project → Security → API Tokens → Generate",
			VarName:   "hcloud_token",
			Regions: []CloudRegion{
				{"nbg1", "Nuremberg (EU)"},
				{"fsn1", "Falkenstein (EU)"},
				{"hel1", "Helsinki (EU)"},
				{"ash", "Ashburn (US East)"},
				{"hil", "Hillsboro (US West)"},
				{"sin", "Singapore (Asia)"},
			},
		},
		{
			Name:      "DigitalOcean",
			Key:       "digitalocean",
			TokenName: "API Token",
			TokenLink: "https://cloud.digitalocean.com/account/api/tokens → Generate New Token",
			VarName:   "do_token",
			Regions: []CloudRegion{
				{"nyc1", "New York 1"},
				{"sfo3", "San Francisco 3"},
				{"ams3", "Amsterdam 3"},
				{"sgp1", "Singapore 1"},
				{"lon1", "London 1"},
				{"fra1", "Frankfurt 1"},
				{"blr1", "Bangalore 1"},
				{"syd1", "Sydney 1"},
			},
		},
		{
			Name:      "AWS",
			Key:       "aws",
			TokenName: "Access Key",
			TokenLink: "https://console.aws.amazon.com/iam/ → Users → Security Credentials → Create Access Key",
			Regions: []CloudRegion{
				{"us-east-1", "US East (N. Virginia)"},
				{"us-east-2", "US East (Ohio)"},
				{"us-west-1", "US West (N. California)"},
				{"us-west-2", "US West (Oregon)"},
				{"eu-west-1", "EU (Ireland)"},
				{"eu-central-1", "EU (Frankfurt)"},
				{"ap-southeast-1", "Asia Pacific (Singapore)"},
				{"ap-northeast-1", "Asia Pacific (Tokyo)"},
				{"ap-south-1", "Asia Pacific (Mumbai)"},
				{"sa-east-1", "South America (São Paulo)"},
			},
		},
	}
}

// AWSCredsEnv validates an AWS Access Key ID / Secret Access Key pair and maps
// it to the environment variables terraform reads. Both blank means "use the
// AWS credentials already in the environment": the returned map is nil and
// terraform, which inherits this process's environment, resolves credentials
// itself (profiles, SSO, instance roles). Exactly one blank is an error.
func AWSCredsEnv(keyID, secret string) (map[string]string, error) {
	if (keyID == "") != (secret == "") {
		return nil, fmt.Errorf("provide both AWS Access Key ID and Secret Access Key, or leave both blank to use the AWS credentials from your environment")
	}
	if keyID == "" {
		return nil, nil
	}
	return map[string]string{
		"AWS_ACCESS_KEY_ID":     keyID,
		"AWS_SECRET_ACCESS_KEY": secret,
	}, nil
}

// awsProvisionRegion picks the region to pin for an AWS provision: an
// explicit choice always wins; explicit keys with no choice keep the
// historical us-east-1 default; ambient credentials with no choice inherit
// the ambient region (empty = the template's null fallback).
func awsProvisionRegion(explicitKeys bool, chosen string) string {
	if chosen == "" && explicitKeys {
		return "us-east-1"
	}
	return chosen
}

// awsAmbientCredentials reports whether this process can see any ambient AWS
// credential source: static env keys, a named profile, shared credential or
// config files, or web-identity/container credentials. It is a cheap local
// pre-flight only — terraform still does the authoritative resolution.
func awsAmbientCredentials() bool {
	if os.Getenv("AWS_ACCESS_KEY_ID") != "" && os.Getenv("AWS_SECRET_ACCESS_KEY") != "" {
		return true
	}
	if os.Getenv("AWS_PROFILE") != "" || os.Getenv("AWS_ROLE_ARN") != "" ||
		os.Getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI") != "" ||
		os.Getenv("AWS_CONTAINER_CREDENTIALS_FULL_URI") != "" {
		return true
	}
	for _, p := range []string{os.Getenv("AWS_SHARED_CREDENTIALS_FILE"), os.Getenv("AWS_CONFIG_FILE")} {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, p := range []string{filepath.Join(home, ".aws", "credentials"), filepath.Join(home, ".aws", "config")} {
			if _, err := os.Stat(p); err == nil {
				return true
			}
		}
	}
	return false
}

// TestCloudCredentials validates credentials for the given provider.
func (o *Ops) TestCloudCredentials(providerName, token, awsSecret string) error {
	switch providerName {
	case "Hetzner":
		return testHTTPToken("https://api.hetzner.cloud/v1/servers", token)
	case "DigitalOcean":
		return testHTTPToken("https://api.digitalocean.com/v2/account", token)
	case "AWS":
		if _, err := AWSCredsEnv(token, awsSecret); err != nil {
			return err
		}
		if token == "" {
			// Ambient mode: fail fast if no credential source is visible at
			// all, instead of minutes later inside terraform apply.
			if !awsAmbientCredentials() {
				return fmt.Errorf("no AWS credentials found in the environment: set AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY or AWS_PROFILE, or configure ~/.aws/credentials")
			}
			return nil
		}
		if len(token) < 16 {
			return fmt.Errorf("Access Key ID looks too short")
		}
		if len(awsSecret) < 30 {
			return fmt.Errorf("Secret Access Key looks too short")
		}
		return nil
	}
	return fmt.Errorf("unknown provider: %s", providerName)
}

func testHTTPToken(url, token string) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return fmt.Errorf("invalid token (HTTP 401)")
	}
	return nil
}
