//go:build !windows

package cli

import (
	"os"
	"os/signal"
	"syscall"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

func watchTermResize(fd int, session *gossh.Session) {
	sigWinch := make(chan os.Signal, 1)
	signal.Notify(sigWinch, syscall.SIGWINCH)

	go func() {
		for range sigWinch {
			if c, r, err := term.GetSize(fd); err == nil {
				_ = session.WindowChange(r, c)
			}
		}
	}()
}
