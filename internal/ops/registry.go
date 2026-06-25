package ops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tunnelwhisperer/tw/internal/config"
)

// RegisteredServer is one enrolled tenant in the admin registry.
type RegisteredServer struct {
	ServerID   string `json:"server_id"`
	UUID       string `json:"uuid"`
	Hostname   string `json:"hostname"`
	RemotePort int    `json:"remote_port"`
	CACertPEM  string `json:"ca_cert_pem"`
	SSHPubkey  string `json:"ssh_pubkey"`
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
