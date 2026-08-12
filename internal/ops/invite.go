package ops

import (
	"context"
	"fmt"
	"net/http"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/tunnelwhisperer/tw/internal/enroll"
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
