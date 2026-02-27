package ops

import (
	"fmt"
	"net/http"
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

// TestCloudCredentials validates credentials for the given provider.
func (o *Ops) TestCloudCredentials(providerName, token, awsSecret string) error {
	switch providerName {
	case "Hetzner":
		return testHTTPToken("https://api.hetzner.cloud/v1/servers", token)
	case "DigitalOcean":
		return testHTTPToken("https://api.digitalocean.com/v2/account", token)
	case "AWS":
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
