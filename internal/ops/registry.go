package ops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tunnelwhisperer/tw/internal/config"
	gossh "golang.org/x/crypto/ssh"
)

// RegisteredServer is one enrolled tenant in the admin registry.
type RegisteredServer struct {
	ServerID   string `json:"server_id"`
	UUID       string `json:"uuid"`
	Hostname   string `json:"hostname"`
	RemotePort int    `json:"remote_port"`
	CACertPEM  string `json:"ca_cert_pem"`
	SSHPubkey  string `json:"ssh_pubkey"`
	EnrolledAt string `json:"enrolled_at,omitempty"` // UTC RFC3339; "" on pre-existing entries
}

// RegistryDir holds one JSON file per enrolled server.
func RegistryDir() string { return filepath.Join(config.Dir(), "servers") }

// ListServers reads all registered servers.
func (o *Ops) ListServers() ([]RegisteredServer, error) {
	entries, err := os.ReadDir(RegistryDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading registry: %w", err)
	}
	var out []RegisteredServer
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(RegistryDir(), e.Name()))
		if err != nil {
			return nil, err
		}
		var s RegisteredServer
		if json.Unmarshal(data, &s) == nil {
			out = append(out, s)
		}
	}
	return out, nil
}

// AddServer registers a joining server, allocating a unique RemotePort.
func (o *Ops) AddServer(req *JoinRequest) (RegisteredServer, error) {
	osHost, _ := os.Hostname()
	if adminID := deriveServerID(osHost, o.Config().Xray.UUID); req.ServerID == adminID {
		// The admin's own entry is seeded into every render outside the
		// registry; letting it in would make it un-enrollable — tearing the
		// admin's own tenant out of the relay.
		return RegisteredServer{}, fmt.Errorf("server id %q is the relay's own identity; refusing to enroll the relay into itself", req.ServerID)
	}
	list, err := o.ListServers()
	if err != nil {
		return RegisteredServer{}, err
	}
	used := make([]int, 0, len(list))
	for _, s := range list {
		if s.ServerID == req.ServerID {
			return RegisteredServer{}, fmt.Errorf("server %q is already enrolled", req.ServerID)
		}
		used = append(used, s.RemotePort)
	}
	port, err := firstFreeFromBase(20000, used)
	if err != nil {
		return RegisteredServer{}, err
	}
	s := RegisteredServer{
		ServerID: req.ServerID, UUID: req.UUID, Hostname: req.Hostname,
		RemotePort: port, CACertPEM: req.CACertPEM, SSHPubkey: req.SSHPubkey,
		EnrolledAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := os.MkdirAll(RegistryDir(), 0o755); err != nil {
		return RegisteredServer{}, err
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	if err := os.WriteFile(filepath.Join(RegistryDir(), req.ServerID+".json"), data, 0o644); err != nil {
		return RegisteredServer{}, fmt.Errorf("writing registry entry: %w", err)
	}
	return s, nil
}

// ServerDetail is one row of `tw relay get-servers`: registry facts plus the
// live tunnel state observed on the relay.
type ServerDetail struct {
	RegisteredServer
	Path     string // /tw/<server-id>
	TunnelUp bool   // the relay listens on 127.0.0.1:<RemotePort> (its reverse forward)
}

// GetServerDetails combines the registry with ONE live query against the
// relay: `ss -tln` over the tunnelled SSH session. A server's reverse tunnel
// is up iff the relay holds a listener on its allocated port. An empty
// registry returns without dialing; a relay failure is returned as an error
// (no stale table).
func (o *Ops) GetServerDetails() ([]ServerDetail, error) {
	servers, err := o.ListServers()
	if err != nil {
		return nil, err
	}
	if len(servers) == 0 {
		return nil, nil
	}
	var listening map[int]bool
	err = o.RelaySSH(func(client *gossh.Client) error {
		session, err := client.NewSession()
		if err != nil {
			return fmt.Errorf("opening ssh session: %w", err)
		}
		defer session.Close()
		// /proc/net/tcp{,6} instead of `ss`: always present on Linux, no
		// dependency on iproute2 being installed on the relay.
		out, err := session.Output("cat /proc/net/tcp /proc/net/tcp6")
		if err != nil {
			return fmt.Errorf("listing relay listeners (/proc/net/tcp): %w", err)
		}
		listening = parseListeningPorts(string(out))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("querying relay: %w", err)
	}
	out := make([]ServerDetail, 0, len(servers))
	for _, s := range servers {
		out = append(out, ServerDetail{
			RegisteredServer: s,
			Path:             "/tw/" + s.ServerID,
			TunnelUp:         listening[s.RemotePort],
		})
	}
	return out, nil
}

// parseListeningPorts extracts the LISTEN local ports from concatenated
// /proc/net/tcp and /proc/net/tcp6 content: column 2 is hexIP:hexPORT,
// column 4 the state (0A = LISTEN). Header lines don't parse and are skipped.
func parseListeningPorts(procNetTCP string) map[int]bool {
	ports := map[int]bool{}
	for _, line := range strings.Split(procNetTCP, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[3] != "0A" {
			continue
		}
		i := strings.LastIndex(f[1], ":")
		if i < 0 {
			continue
		}
		if p, err := strconv.ParseInt(f[1][i+1:], 16, 32); err == nil {
			ports[int(p)] = true
		}
	}
	return ports
}
