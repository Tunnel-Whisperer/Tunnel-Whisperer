package dashboard

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	gossh "golang.org/x/crypto/ssh"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// wsControl is a JSON control message sent from the browser.
type wsControl struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// apiRelaySSH upgrades to a WebSocket and bridges it to an interactive SSH
// session on the relay server.
func (s *Server) apiRelaySSH(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	// Send a connecting status so the frontend can show feedback.
	conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"status","msg":"connecting to relay..."}`))

	err = s.ops.RelaySSH(func(client *gossh.Client) error {
		session, err := client.NewSession()
		if err != nil {
			return err
		}
		defer session.Close()

		// Request a PTY matching the CLI behavior.
		if err := session.RequestPty("xterm-256color", 24, 80, gossh.TerminalModes{
			gossh.ECHO:          1,
			gossh.TTY_OP_ISPEED: 14400,
			gossh.TTY_OP_OSPEED: 14400,
		}); err != nil {
			return err
		}

		stdin, err := session.StdinPipe()
		if err != nil {
			return err
		}

		stdout, err := session.StdoutPipe()
		if err != nil {
			return err
		}

		if err := session.Shell(); err != nil {
			return err
		}

		conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"status","msg":"connected"}`))

		var wg sync.WaitGroup
		done := make(chan struct{})

		// SSH stdout → WebSocket.
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 4096)
			for {
				n, err := stdout.Read(buf)
				if n > 0 {
					if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
						return
					}
				}
				if err != nil {
					return
				}
			}
		}()

		// WebSocket → SSH stdin + control messages.
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(done)
			for {
				msgType, data, err := conn.ReadMessage()
				if err != nil {
					return
				}
				switch msgType {
				case websocket.BinaryMessage:
					if _, err := stdin.Write(data); err != nil {
						return
					}
				case websocket.TextMessage:
					var ctrl wsControl
					if json.Unmarshal(data, &ctrl) == nil && ctrl.Type == "resize" {
						session.WindowChange(ctrl.Rows, ctrl.Cols)
					}
				}
			}
		}()

		// Wait for the SSH session to finish or the WebSocket to close.
		sessionDone := make(chan error, 1)
		go func() {
			sessionDone <- session.Wait()
		}()

		select {
		case <-done:
			// WebSocket closed — signal SSH to exit.
			stdin.Close()
		case err := <-sessionDone:
			// SSH session ended.
			if err != nil && err != io.EOF {
				slog.Debug("relay SSH session ended", "error", err)
			}
		}

		wg.Wait()
		return nil
	})

	if err != nil {
		slog.Error("relay SSH failed", "error", err)
		conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","msg":"`+err.Error()+`"}`))
	}
}
