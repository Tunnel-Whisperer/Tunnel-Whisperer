package api

import (
	"context"
	"os"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/tunnelwhisperer/tw/internal/config"
)

func TestAuthUnaryInterceptor(t *testing.T) {
	const token = "s3cr3t-token"
	interceptor := authUnaryInterceptor(token)

	called := false
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		called = true
		return "ok", nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/api.v1.TunnelWhisperer/GetStatus"}

	ctxWith := func(pairs ...string) context.Context {
		if len(pairs) == 0 {
			return context.Background() // no metadata at all
		}
		return metadata.NewIncomingContext(context.Background(), metadata.Pairs(pairs...))
	}

	t.Run("valid token passes", func(t *testing.T) {
		called = false
		out, err := interceptor(ctxWith("authorization", "Bearer "+token), nil, info, handler)
		if err != nil || out != "ok" || !called {
			t.Fatalf("valid token should reach the handler: out=%v err=%v called=%v", out, err, called)
		}
	})

	deny := map[string]context.Context{
		"no metadata":     ctxWith(),
		"no auth header":  ctxWith("x-other", "y"),
		"wrong token":     ctxWith("authorization", "Bearer wrong"),
		"missing bearer":  ctxWith("authorization", token),
		"empty value":     ctxWith("authorization", ""),
		"prefix only":     ctxWith("authorization", "Bearer "),
		"case-diff scheme": ctxWith("authorization", "bearer "+token),
	}
	for name, ctx := range deny {
		t.Run("rejects "+name, func(t *testing.T) {
			called = false
			_, err := interceptor(ctx, nil, info, handler)
			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("want Unauthenticated, got %v", err)
			}
			if called {
				t.Fatal("handler must not run when auth fails")
			}
		})
	}
}

func TestTokenCreds(t *testing.T) {
	md, err := tokenCreds{token: "abc"}.GetRequestMetadata(context.Background())
	if err != nil || md["authorization"] != "Bearer abc" {
		t.Fatalf("tokenCreds should emit the bearer header, got %v, %v", md, err)
	}
	// An empty token emits no header (the server then rejects "missing").
	md, err = tokenCreds{token: ""}.GetRequestMetadata(context.Background())
	if err != nil || md != nil {
		t.Fatalf("empty token should emit no metadata, got %v, %v", md, err)
	}
	if (tokenCreds{}).RequireTransportSecurity() {
		t.Fatal("tokenCreds must allow the insecure loopback transport")
	}
}

func TestEnsureAPITokenIsStableAnd0600(t *testing.T) {
	t.Setenv("TW_CONFIG_DIR", t.TempDir())
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}

	tok1, err := EnsureAPIToken()
	if err != nil || tok1 == "" {
		t.Fatalf("EnsureAPIToken: %q, %v", tok1, err)
	}
	tok2, err := EnsureAPIToken()
	if err != nil || tok2 != tok1 {
		t.Fatalf("EnsureAPIToken must be stable: %q vs %q (%v)", tok1, tok2, err)
	}
	got, err := readAPIToken()
	if err != nil || got != tok1 {
		t.Fatalf("readAPIToken = %q, %v; want %q", got, err, tok1)
	}

	fi, err := os.Stat(config.APITokenPath())
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("API token file perms = %o, want 600", perm)
	}
}
