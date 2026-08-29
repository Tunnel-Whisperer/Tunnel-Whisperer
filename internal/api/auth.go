package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/tunnelwhisperer/tw/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// The gRPC control plane is loopback-only (finding #5), but loopback is not an
// auth boundary: any local process/user can reach 127.0.0.1 and, without this,
// invoke privileged RPCs (overwrite keys/config, flip mode, delete users) and
// read secrets (finding SP-9, and SP-17 for GetConfig). Every RPC therefore
// requires a per-daemon bearer token held in a 0600 file — the same shape as
// the dashboard token — that only the operator's own uid can read.

const apiTokenBytes = 32

// EnsureAPIToken returns the daemon's API token, generating and persisting it
// (0600) on first use. Called by the server on startup and readable by the CLI,
// which shares the same config dir.
func EnsureAPIToken() (string, error) {
	path := config.APITokenPath()
	if data, err := os.ReadFile(path); err == nil {
		if tok := strings.TrimSpace(string(data)); tok != "" {
			return tok, nil
		}
	}
	buf := make([]byte, apiTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating API token: %w", err)
	}
	tok := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("writing API token: %w", err)
	}
	return tok, nil
}

// unmatchableToken returns a random token used when the real one cannot be
// established, so the interceptor rejects everything instead of degrading to an
// empty (potentially weak) comparison.
func unmatchableToken() string {
	buf := make([]byte, apiTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "\x00unavailable" // still never matches a real bearer value
	}
	return hex.EncodeToString(buf)
}

// readAPIToken reads the current token without creating one; used by the client.
func readAPIToken() (string, error) {
	data, err := os.ReadFile(config.APITokenPath())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// authUnaryInterceptor rejects any RPC that does not present the exact bearer
// token, in constant time.
func authUnaryInterceptor(token string) grpc.UnaryServerInterceptor {
	want := []byte("Bearer " + token)
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing API credentials")
		}
		vals := md.Get("authorization")
		if len(vals) != 1 || subtle.ConstantTimeCompare([]byte(vals[0]), want) != 1 {
			return nil, status.Error(codes.Unauthenticated, "invalid or missing API token")
		}
		return handler(ctx, req)
	}
}

// tokenCreds attaches the bearer token to every client RPC. It permits use over
// an insecure (loopback) transport — the token, not TLS, is the auth here.
type tokenCreds struct{ token string }

func (t tokenCreds) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	if t.token == "" {
		return nil, nil
	}
	return map[string]string{"authorization": "Bearer " + t.token}, nil
}

func (tokenCreds) RequireTransportSecurity() bool { return false }
