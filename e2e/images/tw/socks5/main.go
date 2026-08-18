// Command socks5-server is the e2e upstream-proxy hop: a minimal
// no-auth CONNECT-only SOCKS5 server that logs every CONNECT target, so the
// suite can prove tw's `proxy set socks5://...` really routes the tunnel
// through it (the log must show the relay as the dialed target).
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
)

func main() {
	port := flag.String("port", "1080", "listen port")
	flag.Parse()
	l, err := net.Listen("tcp", ":"+*port)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("socks5 listening on :%s", *port)
	for {
		c, err := l.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go serve(c)
	}
}

func serve(c net.Conn) {
	defer c.Close()
	target, err := handshake(c)
	if err != nil {
		log.Printf("handshake from %s failed: %v", c.RemoteAddr(), err)
		return
	}
	up, err := net.Dial("tcp", target)
	if err != nil {
		log.Printf("CONNECT %s from %s: dial failed: %v", target, c.RemoteAddr(), err)
		// reply: connection refused
		c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	log.Printf("CONNECT %s from %s", target, c.RemoteAddr())
	// reply: succeeded, bound to 0.0.0.0:0 (clients ignore the bind addr)
	if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() { io.Copy(up, c); done <- struct{}{} }()
	go func() { io.Copy(c, up); done <- struct{}{} }()
	<-done
}

// handshake performs the RFC 1928 greeting + request and returns the CONNECT
// target as host:port.
func handshake(c net.Conn) (string, error) {
	// greeting: VER NMETHODS METHODS...
	head := make([]byte, 2)
	if _, err := io.ReadFull(c, head); err != nil {
		return "", err
	}
	if head[0] != 5 {
		return "", fmt.Errorf("not SOCKS5 (ver %d)", head[0])
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(c, methods); err != nil {
		return "", err
	}
	if _, err := c.Write([]byte{5, 0}); err != nil { // no auth
		return "", err
	}
	// request: VER CMD RSV ATYP DST.ADDR DST.PORT
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil {
		return "", err
	}
	if req[1] != 1 {
		return "", fmt.Errorf("unsupported command %d (only CONNECT)", req[1])
	}
	var host string
	switch req[3] {
	case 1: // IPv4
		a := make([]byte, 4)
		if _, err := io.ReadFull(c, a); err != nil {
			return "", err
		}
		host = net.IP(a).String()
	case 3: // domain
		n := make([]byte, 1)
		if _, err := io.ReadFull(c, n); err != nil {
			return "", err
		}
		d := make([]byte, int(n[0]))
		if _, err := io.ReadFull(c, d); err != nil {
			return "", err
		}
		host = string(d)
	case 4: // IPv6
		a := make([]byte, 16)
		if _, err := io.ReadFull(c, a); err != nil {
			return "", err
		}
		host = net.IP(a).String()
	default:
		return "", fmt.Errorf("unsupported address type %d", req[3])
	}
	p := make([]byte, 2)
	if _, err := io.ReadFull(c, p); err != nil {
		return "", err
	}
	return net.JoinHostPort(host, fmt.Sprint(binary.BigEndian.Uint16(p))), nil
}
