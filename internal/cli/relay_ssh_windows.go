//go:build windows

package cli

import gossh "golang.org/x/crypto/ssh"

func watchTermResize(fd int, session *gossh.Session) {}
