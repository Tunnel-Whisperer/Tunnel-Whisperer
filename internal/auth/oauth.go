package auth

import "context"

// Credentials represents client authentication credentials.
type Credentials struct {
	Username     string
	Password     string
	ClientID     string
	ClientSecret string
}

// Claims represents the decoded claims from a JWT.
type Claims struct {
	Subject string
	Scopes  []string
}

// OAuthProvider defines the interface for OAuth authentication.
type OAuthProvider interface {
	// Authenticate validates credentials and returns a JWT token string.
	Authenticate(ctx context.Context, creds Credentials) (token string, err error)

	// ValidateToken verifies a JWT and returns its claims.
	ValidateToken(ctx context.Context, token string) (*Claims, error)
}
