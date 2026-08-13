package ops

import (
	"context"
	"encoding/json"
	"fmt"
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
	srv := &http.Server{Handler: h}
	go srv.Serve(ln) //nolint:errcheck — closed via srv.Close below
	defer srv.Close()
	defer ln.Close()
	return run()
}

// InviteServer mints a one-time invite code, waits for a `tw join` from the
// enrollee through the relay, verifies the SAS with the human, and runs the
// real EnrollServer. The invite burns on the first redemption attempt and
// expires after ttl. Returns the JoinResponse that was granted.
func (o *Ops) InviteServer(ttl time.Duration, ui InviteUI, progress ProgressFunc) (*JoinResponse, error) {
	cfg := o.Config()
	if cfg.Xray.RelayHost == "" {
		return nil, fmt.Errorf("no relay configured — run 'tw relay create' first")
	}
	inv, err := enroll.Mint(first8(cfg.Xray.UUID), ttl)
	if err != nil {
		return nil, err
	}
	h := enroll.NewHandler(inv, enroll.RoleOffer{Role: "server"})
	ui.ShowCode(inv.Code, inv.Expires)

	var resp *JoinResponse
	err = withRelaySSH(cfg, func(client *gossh.Client) error {
		return serveInvite(client, enrollPort(cfg.Server.RemotePort), h, func() error {
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
			// Hold the channel open until the enrollee collects the grant —
			// but never past a short grace window.
			cctx, ccancel := context.WithTimeout(ctx, 60*time.Second)
			defer ccancel()
			if err := h.AwaitCollected(cctx); err != nil {
				return fmt.Errorf("enrollee never collected the grant (tenant %s IS enrolled; un-enroll it if this was abandoned): %w", req.ServerID, err)
			}
			resp = r
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// InviteUser mints a one-time code that enrolls a remote CLIENT for the named
// user: the enrollee generates its SSH key and a cert CSR locally (no private
// key ever transits), we sign the CSR, register the UUID and pubkey, and send
// the client's coordinates down the encrypted channel. Server-mode issuer.
func (o *Ops) InviteUser(req CreateUserRequest, ttl time.Duration, ui InviteUI, progress ProgressFunc) error {
	cfg := o.Config()
	if err := validateCreateUser(*cfg, req); err != nil {
		return err
	}
	inv, err := enroll.Mint(first8(cfg.Xray.UUID), ttl)
	if err != nil {
		return err
	}
	h := enroll.NewHandler(inv, enroll.RoleOffer{Role: "client", Username: req.Name})
	ui.ShowCode(inv.Code, inv.Expires)

	return withRelaySSH(cfg, func(client *gossh.Client) error {
		return serveInvite(client, enrollPort(cfg.Server.RemotePort), h, func() error {
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
			if _, _, _, _, err := gossh.ParseAuthorizedKey([]byte(off.SSHPubkey)); err != nil {
				h.Deny()
				return fmt.Errorf("offer ssh_pubkey invalid: %w", err)
			}
			if !ui.ConfirmSAS(h.SAS()) {
				h.Deny()
				return fmt.Errorf("enrollment denied: SAS read-back did not match")
			}
			grant, err := o.grantClient(*cfg, req, &off)
			if err != nil {
				h.Deny()
				return err
			}
			if err := h.Grant(grant); err != nil {
				return err
			}
			cctx, ccancel := context.WithTimeout(ctx, 60*time.Second)
			defer ccancel()
			if err := h.AwaitCollected(cctx); err != nil {
				return fmt.Errorf("enrollee never collected the grant (user %q IS created; delete it if abandoned): %w", req.Name, err)
			}
			return nil
		})
	})
}

// grantClient performs the server-side creation for an invited client — the
// zero-file sibling of CreateUser: same UUID/authorized_keys/user-dir writes,
// but the key material is the ENROLLEE's public half and the cert is issued
// from their CSR. Rolls itself back on failure.
func (o *Ops) grantClient(cfg config.Config, req CreateUserRequest, off *enroll.ClientOffer) (out []byte, err error) {
	host, _ := os.Hostname()
	serverID := deriveServerID(host, cfg.Xray.UUID)
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
		Client: config.ClientConfig{SSHUser: req.Name, ServerSSHPort: cfg.Server.RemotePort, Tunnels: cfgTunnels},
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
		SSHUser: req.Name, ServerSSHPort: cfg.Server.RemotePort,
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
