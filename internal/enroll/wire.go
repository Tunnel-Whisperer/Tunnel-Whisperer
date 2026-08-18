package enroll

// startReq/startResp, boxMsg are the wire envelopes.
type startReq struct {
	Pake string `json:"pake"` // base64 std
}
type startResp struct {
	Pake string `json:"pake"`
	Box  string `json:"box"` // boxed RoleOffer
}
type boxMsg struct {
	Box string `json:"box"`
}

type RoleOffer struct {
	Role     string `json:"role"` // "server" or "client"
	Username string `json:"username,omitempty"`
}

type ClientOffer struct {
	Username  string `json:"username"`
	UUID      string `json:"uuid"`
	SSHPubkey string `json:"ssh_pubkey"`
	CSRPEM    string `json:"csr_pem"`
}

type GrantTunnel struct {
	LocalPort  int    `json:"local_port"`
	RemoteHost string `json:"remote_host"`
	RemotePort int    `json:"remote_port"`
}

type ClientGrant struct {
	RelayHost     string        `json:"relay_host"`
	RelayPort     int           `json:"relay_port"`
	Path          string        `json:"path"`
	SSHUser       string        `json:"ssh_user"`
	ServerSSHPort int           `json:"server_ssh_port"`
	Tunnels       []GrantTunnel `json:"tunnels"`
	ClientCertPEM string        `json:"client_cert_pem"`
	ModeSig       string        `json:"mode_sig,omitempty"`
	ModeIssuer    string        `json:"mode_issuer,omitempty"`
}
