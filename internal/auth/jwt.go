package auth

import (
	"context"
	"fmt"
)

// JWTProvider is a stub OAuth provider using local JWT issuance.
type JWTProvider struct{}

func NewJWTProvider() *JWTProvider {
	return &JWTProvider{}
}

func (p *JWTProvider) Authenticate(ctx context.Context, creds Credentials) (string, error) {
	return "", fmt.Errorf("jwt: Authenticate not yet implemented")
}

func (p *JWTProvider) ValidateToken(ctx context.Context, token string) (*Claims, error) {
	return nil, fmt.Errorf("jwt: ValidateToken not yet implemented")
}
