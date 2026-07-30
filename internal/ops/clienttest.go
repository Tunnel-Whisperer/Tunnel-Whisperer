package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tunnelwhisperer/tw/internal/config"
	twxray "github.com/tunnelwhisperer/tw/internal/xray"
	gossh "golang.org/x/crypto/ssh"
)

// testClientSSH proves the client path end-to-end: a temporary Xray client
// tunnel to the relay, then an SSH publickey handshake to the server's
// embedded SSH as the tunnel user. Tenant users are forward-only, so a
// successful auth (no exec) is the proof. The per-user key is never valid
// on the relay VM — that path belongs to relay/server roles only.
func testClientSSH(cfg *config.Config) error {
	applyClientCertPaths(&cfg.Xray)
	xrayInstance, err := twxray.NewClient(cfg.Xray)
	if err != nil {
		return fmt.Errorf("initializing Xray: %w", err)
	}
	listenPort, err := freeLoopbackPort()
	if err != nil {
		return fmt.Errorf("allocating local tunnel port: %w", err)
	}
	if err := xrayInstance.StartClient(cfg.Client, listenPort, cfg.Proxy); err != nil {
		return fmt.Errorf("starting Xray: %w", err)
	}
	defer xrayInstance.Close()

	privPath := filepath.Join(config.Dir(), "id_ed25519")
	keyData, err := os.ReadFile(privPath)
	if err != nil {
		return fmt.Errorf("reading client key: %w", err)
	}
	signer, err := gossh.ParsePrivateKey(keyData)
	if err != nil {
		return fmt.Errorf("parsing client key: %w", err)
	}
	sshCfg := &gossh.ClientConfig{
		User:            cfg.Client.SSHUser,
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}
	xrayAddr := fmt.Sprintf("127.0.0.1:%d", listenPort)
	var client *gossh.Client
	for i := 0; i < 15; i++ {
		client, err = gossh.Dial("tcp", xrayAddr, sshCfg)
		if err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return fmt.Errorf("SSH auth to server: %w", err)
	}
	client.Close()
	return nil
}
