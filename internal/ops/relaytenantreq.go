package ops

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/tunnelwhisperer/tw/internal/config"
	relaytenant "github.com/tunnelwhisperer/tw/internal/relay/tenant"
	gossh "golang.org/x/crypto/ssh"
)

// isRelayAdminProfile reports whether cfg is the relay admin's own profile,
// which logs in to the relay as the admin account (sudo). Every other
// profile that talks to the relay is an enrolled server (a tenant): its
// relay account has no shell and reaches the relay only through the tenant
// request handler (relayTenantRequest).
func isRelayAdminProfile(cfg *config.Config) bool {
	return cfg.Mode == "relay"
}

// maxTenantReply bounds what a tenant request reads back from the relay.
const maxTenantReply = 1 << 20

// relayTenantRequest sends one request line ("add <uuid>", "remove <uuid>"
// or "list") to the relay's tenant handler over client, an SSH connection
// authenticated as this server's tenant account, and returns the reply text
// after "ok". The relay forces tw-tenant-request for every session of a
// tenant account, so the exec command below only matters on a relay that
// predates tenant accounts, where it fails with a clear error instead of
// running anything else.
func relayTenantRequest(client *gossh.Client, req string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("opening ssh session: %w", err)
	}
	defer session.Close()
	session.Stdin = strings.NewReader(req + "\n")
	stdout, err := session.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("relay tenant request: %w", err)
	}
	var stderr strings.Builder
	session.Stderr = &stderr
	if err := session.Start(relaytenant.RequestPath); err != nil {
		return "", fmt.Errorf("relay tenant request: %w", err)
	}
	out, readErr := io.ReadAll(io.LimitReader(stdout, maxTenantReply))
	runErr := session.Wait()

	reply, _, _ := strings.Cut(string(out), "\n")
	verb, _, _ := strings.Cut(req, " ")
	switch {
	case reply == "ok":
		return "", nil
	case strings.HasPrefix(reply, "ok "):
		return strings.TrimPrefix(reply, "ok "), nil
	case strings.HasPrefix(reply, "err "):
		return "", fmt.Errorf("relay refused %q request: %s", verb, strings.TrimPrefix(reply, "err "))
	case readErr != nil:
		return "", fmt.Errorf("relay tenant request %q: reading reply: %w", verb, readErr)
	case runErr != nil:
		return "", fmt.Errorf("relay tenant request %q failed: %w (stderr: %q) — if the relay predates per-tenant accounts, its admin must run 'tw relay sync-tenants'",
			verb, runErr, strings.TrimSpace(stderr.String()))
	default:
		return "", fmt.Errorf("relay tenant request %q: unexpected reply %q", verb, reply)
	}
}

// relayTenantList returns the client ids on this server's own relay inbound.
func relayTenantList(client *gossh.Client) ([]string, error) {
	reply, err := relayTenantRequest(client, "list")
	if err != nil {
		return nil, err
	}
	return strings.Fields(reply), nil
}

// relayTenantApply runs one tenant request per UUID ("add" or "remove") on a
// single relay connection. A change the relay saved but could not apply to
// its running Xray (the handler never restarts Xray) is an error naming the
// affected UUIDs: those users are not (or still) live until the relay's
// Xray restarts, and sending the same request again retries the live part.
func relayTenantApply(cfg *config.Config, verb string, uuids []string) error {
	for _, u := range uuids {
		if !isCanonicalUUID(u) {
			return fmt.Errorf("refusing to send non-canonical UUID %q to the relay", u)
		}
	}
	return withRelaySSH(cfg, func(client *gossh.Client) error {
		return relayTenantApplyOn(client, verb, uuids)
	})
}

func relayTenantApplyOn(client *gossh.Client, verb string, uuids []string) error {
	var notLive []string
	var lastReply string
	for _, u := range uuids {
		reply, err := relayTenantRequest(client, verb+" "+u)
		if err != nil {
			return err
		}
		if strings.Contains(reply, relaytenant.LiveApplyFailed) {
			slog.Warn("relay saved the tenant request but could not apply it live", "request", verb, "uuid", u[:8], "reply", reply)
			notLive = append(notLive, u[:8])
			lastReply = reply
			continue
		}
		slog.Info("relay tenant request", "request", verb, "uuid", u[:8], "reply", reply)
	}
	if len(notLive) > 0 {
		return fmt.Errorf("relay saved the %s of %s but could not apply it to its running Xray (%s): it takes effect when the relay's Xray restarts; retry, or ask the relay admin to restart xray",
			verb, strings.Join(notLive, ", "), lastReply)
	}
	return nil
}

// tenantRelayUserFallback returns the relay account a server should retry
// with when its stored relay user rejects its key, or "" when there is
// nothing different to try. Servers enrolled before per-tenant relay accounts
// stored the admin's account; the relay no longer accepts tenant keys there.
func tenantRelayUserFallback(cfg *config.Config) string {
	if isRelayAdminProfile(cfg) {
		return ""
	}
	u := relaytenant.UserName(resolveServerID(cfg, config.ClientCertPath()))
	if u == cfg.Server.RelaySSHUser {
		return ""
	}
	return u
}

// isSSHAuthFailure reports whether err is the SSH server rejecting every
// offered credential (as opposed to a transport failure).
func isSSHAuthFailure(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unable to authenticate")
}

// saveTenantRelayUser persists the per-tenant relay account user, which
// authenticated in place of the stored, pre-migration relay user from, into
// the saved profile cfg was loaded from. Only that one field changes, and
// only while the active profile on disk is still cfg's (no context switch
// since) and still stores from.
func saveTenantRelayUser(cfg *config.Config, from, user string) error {
	saved, err := config.Load()
	if err != nil {
		return err
	}
	if saved.Mode != cfg.Mode || saved.Xray.UUID != cfg.Xray.UUID ||
		saved.Xray.ServerID != cfg.Xray.ServerID || saved.Xray.RelayHost != cfg.Xray.RelayHost {
		return fmt.Errorf("the active profile is no longer the one that connected to the relay (context switched?); not saving relay user %s into it", user)
	}
	switch saved.Server.RelaySSHUser {
	case user:
		return nil
	case from:
	default:
		return fmt.Errorf("the saved relay user changed to %q meanwhile; not replacing it with %s", saved.Server.RelaySSHUser, user)
	}
	saved.Server.RelaySSHUser = user
	return config.Save(saved)
}

// liveOps holds every Ops of this process (see trackLiveOps).
var liveOps sync.Map // *Ops -> struct{}

// trackLiveOps lets dialRelaySSH bring o's in-memory profile along when it
// adopts a tenant account: callers usually dial with a copy (o.Config()),
// and o.cfg still holding the old user would be written back by the next
// save of the profile.
func trackLiveOps(o *Ops) { liveOps.Store(o, struct{}{}) }

// followTenantRelayUser switches every live Ops whose profile is cfg's from
// relay user from to user. Asynchronous: the dialing caller may hold o.mu.
func followTenantRelayUser(cfg *config.Config, from, user string) {
	uuid, serverID := cfg.Xray.UUID, cfg.Xray.ServerID
	liveOps.Range(func(k, _ any) bool {
		o := k.(*Ops)
		go func() {
			o.mu.Lock()
			defer o.mu.Unlock()
			if o.cfg.Xray.UUID == uuid && o.cfg.Xray.ServerID == serverID && o.cfg.Server.RelaySSHUser == from {
				o.cfg.Server.RelaySSHUser = user
			}
		}()
		return true
	})
}

// adoptTenantRelayUser is the running server's saveTenantRelayUser: it also
// updates the in-memory profile, so later relay connections of this process
// (and a later config save) use the tenant account.
func (o *Ops) adoptTenantRelayUser(user string) {
	o.mu.Lock()
	from := o.cfg.Server.RelaySSHUser
	o.cfg.Server.RelaySSHUser = user
	snapshot := *o.cfg
	o.mu.Unlock()
	if err := saveTenantRelayUser(&snapshot, from, user); err != nil {
		slog.Error("could not save the tenant relay account to the profile", "user", user, "error", err)
		return
	}
	slog.Info("relay ssh user migrated to the server's tenant account", "user", user)
}

// dialRelaySSH opens the SSH connection to the relay's sshd at addr as
// cfg.Server.RelaySSHUser. On a server profile whose stored user rejects the
// key it retries once as the server's own tenant account and, if that works,
// adopts it (cfg and the saved profile) so later connections use it directly.
func dialRelaySSH(addr string, cfg *config.Config, signer gossh.Signer, hostKeyCallback gossh.HostKeyCallback, timeout time.Duration) (*gossh.Client, error) {
	dial := func(user string) (*gossh.Client, error) {
		return gossh.Dial("tcp", addr, &gossh.ClientConfig{
			User:            user,
			Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
			HostKeyCallback: hostKeyCallback,
			Timeout:         timeout,
		})
	}
	client, err := dial(cfg.Server.RelaySSHUser)
	if !isSSHAuthFailure(err) {
		return client, err
	}
	fallback := tenantRelayUserFallback(cfg)
	if fallback == "" {
		return nil, err
	}
	slog.Warn("relay rejected this server's key for the stored relay user; retrying with the server's own tenant account",
		"user", cfg.Server.RelaySSHUser, "tenant_user", fallback)
	client, ferr := dial(fallback)
	if ferr != nil {
		return nil, fmt.Errorf("%w (tenant account %s: %v)", err, fallback, ferr)
	}
	from := cfg.Server.RelaySSHUser
	cfg.Server.RelaySSHUser = fallback
	followTenantRelayUser(cfg, from, fallback)
	if perr := saveTenantRelayUser(cfg, from, fallback); perr != nil {
		slog.Error("could not save the tenant relay account to the profile", "user", fallback, "error", perr)
	} else {
		slog.Info("relay ssh user migrated to the server's tenant account", "user", fallback)
	}
	return client, nil
}
