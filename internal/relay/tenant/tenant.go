// Package tenant holds the relay-side pieces that let an enrolled server (a
// tenant) manage its own Xray client list without any shell on the relay:
//
//   - one Unix account per tenant (UserName), whose single authorized_keys
//     line forces RequestPath and only permits the tenant's own reverse
//     listeners;
//   - tw-tenant-request (RequestPath), which forwards one request line to
//     the account's own socket /run/tw/tenant/<account>.sock;
//   - tw-tenant@.socket / tw-tenant@.service, a socket-activated root
//     handler that identifies the tenant from the socket path and the peer
//     uid and edits only that tenant's vless-in-<server-id> inbound;
//   - an sshd Match block repeating the restriction for twt-* accounts.
//
// RenderSync renders the idempotent root script that installs all of it,
// reconciles the tenant accounts with the admin's registry and, given the
// admin account, rewrites that account's authorized_keys last.
package tenant

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"text/template"
)

// Paths on the relay.
const (
	RequestPath = "/usr/local/lib/tw/tw-tenant-request"
	HandlerPath = "/usr/local/lib/tw/tw-tenant-handler"
	// XrayConfigLockPath serializes every read-modify-write of the relay's
	// Xray config.json between the tenant handler and the admin.
	XrayConfigLockPath = "/usr/local/etc/xray/.tw-config.lock"
	SSHDDropInPath     = "/etc/ssh/sshd_config.d/98-tw-tenants.conf"
	homeBase           = "/var/lib/tw/tenants"
)

// LiveApplyFailed marks a tenant handler "ok" reply for a change that was
// saved to the relay's Xray config but not applied to the running Xray.
const LiveApplyFailed = "live apply failed"

var (
	//go:embed tw-tenant-handler.py
	handlerScript []byte
	//go:embed tw-tenant-request.py
	requestScript []byte
	//go:embed tw-tenant@.socket
	socketUnit []byte
	//go:embed tw-tenant@.service
	serviceUnit []byte
	//go:embed 98-tw-tenants.conf
	sshdDropIn []byte
	//go:embed sync.sh.tmpl
	syncTmplText string

	syncTmpl = template.Must(template.New("sync.sh").Parse(syncTmplText))

	// serverIDRe is the server-id shape every relay renderer enforces.
	serverIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	// adminUserRe is a portable Linux user name.
	adminUserRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

// UserName is the relay Unix account of the tenant with this server id:
// "twt-" + the first 24 hex digits of sha256(server id). Deterministic, so
// both the admin (creating it) and the tenant (logging in) derive it; 28
// characters, a valid Linux user name (at most 32). tw-tenant-handler checks
// the same rule.
func UserName(serverID string) string {
	sum := sha256.Sum256([]byte(serverID))
	return "twt-" + hex.EncodeToString(sum[:])[:24]
}

// File is one relay-side file installed by RenderSync.
type File struct {
	Path    string
	Mode    string
	Content []byte
}

// B64 is the file content, base64-encoded for embedding in a shell script.
func (f File) B64() string { return base64.StdEncoding.EncodeToString(f.Content) }

// Files returns the static relay-side files, in install order.
func Files() []File {
	return []File{
		{Path: HandlerPath, Mode: "0755", Content: handlerScript},
		{Path: RequestPath, Mode: "0755", Content: requestScript},
		{Path: "/etc/systemd/system/tw-tenant@.socket", Mode: "0644", Content: socketUnit},
		{Path: "/etc/systemd/system/tw-tenant@.service", Mode: "0644", Content: serviceUnit},
		{Path: SSHDDropInPath, Mode: "0644", Content: sshdDropIn},
	}
}

// Account is one enrolled server's relay account.
type Account struct {
	ServerID string
	// AuthorizedKeys is the account's complete authorized_keys content.
	AuthorizedKeys string
	// Ports are the loopback ports the tenant may listen on (reverse tunnel
	// and enroll listener). Any session holding one of them under another
	// account is ended by the sync.
	Ports []int
}

// Admin is the relay admin account whose authorized_keys the sync rewrites
// once every tenant has its own account.
type Admin struct {
	User string
	// AuthorizedKeys is the account's complete authorized_keys content.
	AuthorizedKeys string
}

type adminView struct {
	User              string
	AuthorizedKeysB64 string
}

type accountView struct {
	User              string
	ServerID          string
	ServerIDB64       string
	AuthorizedKeysB64 string
	Ports             []int
}

// RenderSync renders the root shell script (run as `sudo sh -s`) that
// installs Files and makes the relay's tenant accounts exactly accounts:
// missing ones are created, existing ones get their authorized_keys and
// server-id mapping rewritten, and twt-* accounts not listed are removed
// together with their processes, socket and home.
//
// With admin set, the script then rewrites the admin account's
// authorized_keys — after the tenant accounts exist, so a failed sync never
// locks out a tenant that still logs in as the admin — and, if that changed
// the file, ends the admin's other sshd sessions: one opened with a tenant
// key that just left the file must not stay usable. Provisioning passes nil
// (it writes the admin's keys itself, and there are no sessions yet).
func RenderSync(admin *Admin, accounts []Account) (string, error) {
	var av *adminView
	if admin != nil {
		if !adminUserRe.MatchString(admin.User) {
			return "", fmt.Errorf("tenant: invalid admin account name %q", admin.User)
		}
		if admin.AuthorizedKeys == "" || admin.AuthorizedKeys[len(admin.AuthorizedKeys)-1] != '\n' {
			return "", fmt.Errorf("tenant: admin authorized_keys must be non-empty and newline-terminated")
		}
		av = &adminView{User: admin.User, AuthorizedKeysB64: base64.StdEncoding.EncodeToString([]byte(admin.AuthorizedKeys))}
	}
	views := make([]accountView, 0, len(accounts))
	seen := make(map[string]bool, len(accounts))
	for _, a := range accounts {
		if !serverIDRe.MatchString(a.ServerID) {
			return "", fmt.Errorf("tenant: invalid server id %q", a.ServerID)
		}
		user := UserName(a.ServerID)
		if seen[user] {
			return "", fmt.Errorf("tenant: duplicate tenant account %s (server id %q)", user, a.ServerID)
		}
		seen[user] = true
		if a.AuthorizedKeys == "" || bytes.Count([]byte(a.AuthorizedKeys), []byte("\n")) != 1 ||
			a.AuthorizedKeys[len(a.AuthorizedKeys)-1] != '\n' {
			return "", fmt.Errorf("tenant: authorized_keys for %q must be exactly one newline-terminated line", a.ServerID)
		}
		for _, p := range a.Ports {
			if p < 1 || p > 65535 {
				return "", fmt.Errorf("tenant: invalid port %d for %q", p, a.ServerID)
			}
		}
		views = append(views, accountView{
			User:              user,
			ServerID:          a.ServerID,
			ServerIDB64:       base64.StdEncoding.EncodeToString([]byte(a.ServerID + "\n")),
			AuthorizedKeysB64: base64.StdEncoding.EncodeToString([]byte(a.AuthorizedKeys)),
			Ports:             a.Ports,
		})
	}
	var buf bytes.Buffer
	err := syncTmpl.Execute(&buf, struct {
		Files       []File
		Accounts    []accountView
		Admin       *adminView
		HomeBase    string
		SSHDDropIn  string
		RequestPath string
	}{Files(), views, av, homeBase, SSHDDropInPath, RequestPath})
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}
