package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	gossh "golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"

	"github.com/tunnelwhisperer/tw/internal/config"
	"github.com/tunnelwhisperer/tw/internal/enroll"
	"github.com/tunnelwhisperer/tw/internal/ops/modeauth"
	"github.com/tunnelwhisperer/tw/internal/pki"
	twssh "github.com/tunnelwhisperer/tw/internal/ssh"
)

// InviteUI carries the human-in-the-loop callbacks of an invite flow.
type InviteUI struct {
	ShowCode   func(code string, expires time.Time)
	ConfirmSAS func(sas string) bool
}

// serveInvite opens the issuer's enroll listener on the relay loopback over
// the given SSH client and blocks until run returns. run receives the handler
// once the listener is live.
func serveInvite(client *gossh.Client, port int, h *enroll.Handler, run func() error) error {
	ln, err := client.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("opening enroll listener on relay port %d: %w", port, err)
	}
	// ReadHeaderTimeout bounds slow-header attacks on the enrollment endpoint
	// (finding SP-19); the PAKE exchange itself is short, so a modest read
	// timeout is also safe here.
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 15 * time.Second, ReadTimeout: 60 * time.Second}
	go srv.Serve(ln) //nolint:errcheck — closed via srv.Close below
	defer srv.Close()
	defer ln.Close()
	return run()
}

// deliverGrantPhase2 re-serves h — already carrying a sealed grant from a
// phase-1 connection that has since closed — on fresh connection(s) so the
// enrollee's next poll can still collect it. All exchange state lives in h
// itself (internal/enroll.Handler), not the transport, so re-serving it on a
// new listener is correct.
//
// This exists because a phase-1 connection can die between Grant() and the
// enrollee's collection poll for reasons outside the issuer's control: a
// second Caddy reload landing mid-exchange (InviteServer, via EnrollServer)
// or — same mechanism, different trigger — grantClient's addUUIDToRelay
// falling back to a relay xray restart when the hot-add gRPC call is
// rejected (InviteUser). Neither underlying cause is fixed here; this only
// makes grant *delivery* resilient to the connection it kills.
//
// Bounded window: min(60s, invite expiry), computed once at call time and
// held fixed across every reconnect attempt within it (not reset per
// retry) — the same grace-window budget the single-connection design used
// to spend on one connection, now spent across however many reconnects it
// takes. Dial/listen failures (relay briefly unservable, e.g. mid-reload)
// are retried every 2s while the window allows; a clean AwaitCollected
// timeout on a connection that came up fine is terminal. terminalErr builds
// the caller-specific error message, wrapping the triggering cause.
func deliverGrantPhase2(cfg *config.Config, inv *enroll.Invite, h *enroll.Handler, port int, terminalErr func(cause error) error) error {
	deadline := time.Now().Add(60 * time.Second)
	if inv.Expires.Before(deadline) {
		deadline = inv.Expires
	}
	for {
		if !time.Now().Before(deadline) {
			return terminalErr(context.DeadlineExceeded)
		}
		var collected bool
		attemptErr := withRelaySSH(cfg, func(client *gossh.Client) error {
			return serveInvite(client, port, h, func() error {
				cctx, ccancel := context.WithDeadline(context.Background(), deadline)
				defer ccancel()
				if err := h.AwaitCollected(cctx); err != nil {
					return err
				}
				collected = true
				return nil
			})
		})
		if collected {
			return nil
		}
		if errors.Is(attemptErr, context.DeadlineExceeded) {
			// AwaitCollected itself timed out on a connection that was
			// otherwise up — terminal, not a transport hiccup to retry.
			return terminalErr(attemptErr)
		}
		// DIAL/LISTEN failure — the relay is likely briefly unservable
		// (e.g. mid-reload). Sleep and retry while time allows.
		slog.Warn("invite phase-2 grant-delivery connection failed; retrying", "error", attemptErr)
		time.Sleep(2 * time.Second)
	}
}

// InviteServer mints a one-time invite code, waits for a `tw join` from the
// enrollee through the relay, verifies the SAS with the human, and runs the
// real EnrollServer. The invite burns on the first redemption attempt and
// expires after ttl. Returns the JoinResponse that was granted.
//
// Two-phase by design: EnrollServer's own Caddy reload survives on the
// phase-1 connection (measured), but a SECOND reload landing mid-exchange —
// e.g. a concurrent admin op, or (on the e2e relay) the fake-domain
// local_certs shim reapplying — kills that single SSH-over-Xray connection
// outright, since it has no reconnect logic. Phase 1 (mint through Grant)
// runs on one connection and then deliberately returns, closing it. Phase 2
// (deliverGrantPhase2) re-serves the SAME Handler on a fresh connection,
// retrying dial/listen failures, so the enrollee's next poll can still
// collect the grant even if the relay's TLS was briefly unservable across a
// reload.
func (o *Ops) InviteServer(ttl time.Duration, ui InviteUI, progress ProgressFunc) (*JoinResponse, error) {
	// Fail before minting: once the code is shown and redeemed it burns, so a
	// local write failure discovered later would waste it for nothing.
	if err := config.CheckWritable(); err != nil {
		return nil, err
	}
	cfg := o.Config()
	if cfg.Xray.RelayHost == "" {
		return nil, fmt.Errorf("no relay configured — run 'tw relay create' first")
	}
	inv, err := enroll.Mint(first8(cfg.Xray.UUID), ttl)
	if err != nil {
		return nil, err
	}
	h := enroll.NewHandler(inv, enroll.RoleOffer{Role: "server"})
	port := enrollPort(cfg.Server.RemotePort)

	// Phase 1: mint through grant, on the connection that served /start and
	// /offer. Deny()/error semantics on failure paths are unchanged.
	var resp *JoinResponse
	err = withRelaySSH(cfg, func(client *gossh.Client) error {
		return serveInvite(client, port, h, func() error {
			// Show the code only now: the enroll listener is bound, so the
			// code is redeemable the instant the operator can read it. Shown
			// earlier, an eager enrollee races the tunnel setup and gets a
			// dead /enroll route (a bare 404) instead of a PAKE verdict.
			ui.ShowCode(inv.Code, inv.Expires)
			ctx, cancel := context.WithDeadline(context.Background(), inv.Expires)
			defer cancel()
			payload, err := h.AwaitOffer(ctx)
			if err != nil {
				return err
			}
			req, err := DecodeJoinRequest(payload)
			if err != nil {
				h.Deny()
				return fmt.Errorf("invalid enrollment offer: %w", err)
			}
			if !ui.ConfirmSAS(h.SAS()) {
				h.Deny()
				return fmt.Errorf("enrollment denied: SAS read-back did not match")
			}
			r, err := o.EnrollServer(req, progress)
			if err != nil {
				h.Deny()
				return err
			}
			grant, err := r.Encode()
			if err != nil {
				return fmt.Errorf("encoding grant: %w", err)
			}
			if err := h.Grant(grant); err != nil {
				return err
			}
			resp = r
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	if err := deliverGrantPhase2(cfg, inv, h, port, func(cause error) error {
		return fmt.Errorf("enrollee never collected the grant (tenant %s IS enrolled; un-enroll it if this was abandoned): %w", resp.ServerID, cause)
	}); err != nil {
		return nil, err
	}
	return resp, nil
}

// InviteUser mints a one-time code that enrolls a remote CLIENT for the named
// user: the enrollee generates its SSH key and a cert CSR locally (no private
// key ever transits), we sign the CSR, register the UUID and pubkey, and send
// the client's coordinates down the encrypted channel. Server-mode issuer.
//
// Two-phase for the same reason InviteServer is: grantClient's
// addUUIDToRelay hot-adds the UUID via Xray's gRPC API and, on failure,
// falls back to restarting the relay's Xray — which severs every live VLESS
// session there, including phase 1's own serveInvite connection, exactly
// like a second Caddy reload does for InviteServer. Phase 1 (mint through
// Grant) runs on one connection and returns; deliverGrantPhase2 re-serves
// the same Handler on fresh connection(s) so the enrollee still collects the
// grant.
func (o *Ops) InviteUser(req CreateUserRequest, ttl time.Duration, ui InviteUI, progress ProgressFunc) error {
	// Fail before minting: once the code is shown and redeemed it burns, and
	// grantClient has already created the user server-side by then, so a
	// local write failure discovered later would waste both for nothing.
	if err := config.CheckWritable(); err != nil {
		return err
	}
	cfg := o.Config()
	if err := validateCreateUser(*cfg, req); err != nil {
		return err
	}
	inv, err := enroll.Mint(first8(cfg.Xray.UUID), ttl)
	if err != nil {
		return err
	}
	h := enroll.NewHandler(inv, enroll.RoleOffer{Role: "client", Username: req.Name})
	port := enrollPort(cfg.Server.RemotePort)

	// Phase 1: mint through grant, on the connection that served /start and
	// /offer. Deny()/error semantics on failure paths are unchanged.
	err = withRelaySSH(cfg, func(client *gossh.Client) error {
		return serveInvite(client, port, h, func() error {
			// Show the code only now: the enroll listener is bound, so the
			// code is redeemable the instant the operator can read it (see
			// InviteServer for the race this prevents).
			ui.ShowCode(inv.Code, inv.Expires)
			ctx, cancel := context.WithDeadline(context.Background(), inv.Expires)
			defer cancel()
			payload, err := h.AwaitOffer(ctx)
			if err != nil {
				return err
			}
			var off enroll.ClientOffer
			if err := json.Unmarshal(payload, &off); err != nil {
				h.Deny()
				return fmt.Errorf("invalid enrollment offer: %w", err)
			}
			if off.Username != req.Name || off.UUID == "" {
				h.Deny()
				return fmt.Errorf("offer does not match invite (user %q)", req.Name)
			}
			// Canonicalize the enrollee's key and reject any trailing data, then
			// use the normalized value everywhere downstream (id_ed25519.pub,
			// authorized_keys, the undo, and the mode signature) so a newline
			// can't inject an unrestricted authorized_keys line on the server.
			canonKey, err := canonicalAuthorizedKey(off.SSHPubkey)
			if err != nil {
				h.Deny()
				return fmt.Errorf("offer ssh_pubkey invalid: %w", err)
			}
			off.SSHPubkey = canonKey
			if !ui.ConfirmSAS(h.SAS()) {
				h.Deny()
				return fmt.Errorf("enrollment denied: SAS read-back did not match")
			}
			grant, err := o.grantClient(*cfg, req, &off)
			if err != nil {
				h.Deny()
				return err
			}
			return h.Grant(grant)
		})
	})
	if err != nil {
		return err
	}

	return deliverGrantPhase2(cfg, inv, h, port, func(cause error) error {
		return fmt.Errorf("enrollee never collected the grant (user %q IS created; delete it if abandoned): %w", req.Name, cause)
	})
}

// grantClient performs the server-side creation for an invited client —
// UUID/authorized_keys/user-dir writes where the key material is the
// ENROLLEE's public half and the cert is issued from their CSR. Rolls itself
// back on failure.
func (o *Ops) grantClient(cfg config.Config, req CreateUserRequest, off *enroll.ClientOffer) (out []byte, err error) {
	serverID, err := o.serverID()
	if err != nil {
		return nil, err
	}
	caCert, err := os.ReadFile(config.CACertPath())
	if err != nil {
		return nil, fmt.Errorf("reading CA cert: %w", err)
	}
	caKey, err := os.ReadFile(config.CAKeyPath())
	if err != nil {
		return nil, fmt.Errorf("reading CA key: %w", err)
	}
	certPEM, err := pki.SignClientCSR(caCert, caKey, []byte(off.CSRPEM), serverID)
	if err != nil {
		return nil, fmt.Errorf("signing client CSR: %w", err)
	}

	// Everything below mutates state — undo on any later failure.
	var undo []func()
	defer func() {
		if err != nil {
			for i := len(undo) - 1; i >= 0; i-- {
				undo[i]()
			}
		}
	}()

	if err = addUUIDToRelay(&cfg, off.UUID); err != nil {
		return nil, fmt.Errorf("registering UUID on relay: %w", err)
	}
	undo = append(undo, func() { _ = removeUUIDFromRelay(&cfg, off.UUID) })

	// The client pins this to verify the end-to-end SSH session against a
	// relay MITM. Best-effort: an empty pin makes the client fail closed at
	// connect time rather than silently accept any host key.
	serverHostKey, herr := twssh.EnsureHostPublicKey(config.HostKeyDir())
	if herr != nil {
		slog.Warn("could not read server SSH host key for pinning; client must re-enroll once it is available", "error", herr)
	}

	userDir := filepath.Join(config.UsersDir(), req.Name)
	if err = os.MkdirAll(userDir, 0700); err != nil {
		return nil, fmt.Errorf("creating user directory: %w", err)
	}
	undo = append(undo, func() { _ = os.RemoveAll(userDir) })
	// The server keeps ONLY public material for this user — the private keys
	// were born on the client and never travelled.
	if err = os.WriteFile(filepath.Join(userDir, "id_ed25519.pub"), []byte(off.SSHPubkey+"\n"), 0644); err != nil {
		return nil, err
	}
	tunnels := make([]enroll.GrantTunnel, len(req.Mappings))
	serverPorts := make([]int, len(req.Mappings))
	cfgTunnels := make([]config.Tunnel, len(req.Mappings))
	for i, m := range req.Mappings {
		tunnels[i] = enroll.GrantTunnel{LocalPort: m.ClientPort, RemoteHost: "127.0.0.1", RemotePort: m.ServerPort}
		cfgTunnels[i] = config.Tunnel{LocalPort: m.ClientPort, RemoteHost: "127.0.0.1", RemotePort: m.ServerPort}
		serverPorts[i] = m.ServerPort
	}
	// user config.yaml: same shape CreateUser writes (ListUsers reads it back).
	uc := struct {
		Xray   config.XrayConfig   `yaml:"xray"`
		Client config.ClientConfig `yaml:"client"`
	}{
		Xray:   config.XrayConfig{UUID: off.UUID, RelayHost: cfg.Xray.RelayHost, RelayPort: cfg.Xray.RelayPort, Path: cfg.Xray.Path},
		Client: config.ClientConfig{SSHUser: req.Name, ServerSSHPort: cfg.Server.RemotePort, ServerHostKey: serverHostKey, Tunnels: cfgTunnels},
	}
	ucData, err := yaml.Marshal(uc)
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(userDir, "config.yaml"), ucData, 0644); err != nil {
		return nil, err
	}
	if req.SingleSession {
		if err = os.WriteFile(filepath.Join(userDir, ".single-session"), nil, 0644); err != nil {
			return nil, err
		}
	}
	if err = appendAuthorizedKey([]byte(off.SSHPubkey), req.Name, serverPorts, req.SingleSession); err != nil {
		return nil, fmt.Errorf("updating authorized_keys: %w", err)
	}
	undo = append(undo, func() { _ = removeAuthorizedKey([]byte(off.SSHPubkey)) })
	_ = os.WriteFile(filepath.Join(userDir, ".applied"), nil, 0644)

	g := enroll.ClientGrant{
		RelayHost: cfg.Xray.RelayHost, RelayPort: cfg.Xray.RelayPort, Path: cfg.Xray.Path,
		SSHUser: req.Name, ServerSSHPort: cfg.Server.RemotePort, ServerHostKey: serverHostKey,
		Tunnels: tunnels, ClientCertPEM: string(certPEM),
	}
	// Sign the client's mode against ITS pubkey. Best-effort: an unsigned
	// grant still works.
	if priv, perr := profilePrivPEM(); perr == nil {
		if sig, issuer, serr := modeauth.Sign(priv, "client", strings.TrimSpace(off.SSHPubkey)); serr == nil {
			g.ModeSig, g.ModeIssuer = sig, issuer
		}
	}
	return json.Marshal(g)
}
