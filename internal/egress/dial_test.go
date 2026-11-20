package egress

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func noEnv(string) (string, bool) { return "", false }

func mustResolve(t *testing.T, cfg string) Proxy {
	t.Helper()
	p, err := Resolve(cfg, noEnv)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", cfg, err)
	}
	return p
}

func startEcho(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				io.Copy(c, c)
			}()
		}
	}()
	return ln
}

func startServer(t *testing.T, handle func(c net.Conn)) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				handle(c)
			}()
		}
	}()
	return ln
}

func pipeTo(c net.Conn, br *bufio.Reader, target string) error {
	up, err := net.Dial("tcp", target)
	if err != nil {
		return err
	}
	defer up.Close()
	done := make(chan struct{})
	go func() {
		io.Copy(c, up)
		close(done)
	}()
	io.Copy(up, br)
	up.(*net.TCPConn).CloseWrite()
	<-done
	return nil
}

func assertEcho(t *testing.T, c net.Conn) {
	t.Helper()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo = %q, want ping", buf)
	}
}

func TestHTTPTransportProxyFunc(t *testing.T) {
	p := mustResolve(t, "http://p:1")
	tlsCfg := &tls.Config{ServerName: "x"}
	tr := p.HTTPTransport(tlsCfg)
	if tr.TLSClientConfig != tlsCfg {
		t.Fatal("TLSClientConfig not passed through")
	}
	if tr.Proxy == nil {
		t.Fatal("Proxy func is nil for a configured proxy")
	}
	req := &http.Request{URL: &url.URL{Scheme: "https", Host: "relay.tw.test"}}
	got, err := tr.Proxy(req)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Host != "p:1" {
		t.Fatalf("Proxy(relay) = %v, want p:1", got)
	}
	req = &http.Request{URL: &url.URL{Scheme: "https", Host: "127.0.0.1:5000"}}
	if got, _ := tr.Proxy(req); got != nil {
		t.Fatalf("Proxy(loopback) = %v, want nil", got)
	}

	none := Proxy{Source: SourceNone}.HTTPTransport(nil)
	if none.Proxy != nil {
		t.Fatal("Proxy field must be nil when Source is none")
	}
}

func TestHTTPClient(t *testing.T) {
	c := Proxy{Source: SourceNone}.HTTPClient(3*time.Second, nil)
	if c.Timeout != 3*time.Second {
		t.Fatalf("Timeout = %v", c.Timeout)
	}
	if _, ok := c.Transport.(*http.Transport); !ok {
		t.Fatalf("Transport = %T", c.Transport)
	}
}

func TestDialContextDirectWhenNone(t *testing.T) {
	ln := startEcho(t)
	c, err := Proxy{Source: SourceNone}.DialContext(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	assertEcho(t, c)
}

func TestDialContextLoopbackIgnoresProxy(t *testing.T) {
	ln := startEcho(t)
	p := mustResolve(t, "socks5://127.0.0.1:1")
	c, err := p.DialContext(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("loopback target should bypass proxy: %v", err)
	}
	defer c.Close()
	assertEcho(t, c)
}

// socks5Creds is what the in-test SOCKS5 server saw in an RFC 1929 exchange.
type socks5Creds struct{ user, pass string }

// socks5Server is a minimal SOCKS5 CONNECT server. With wantUser set it
// requires username/password auth (method 0x02) and records what it saw on
// gotCreds; recorded receives the CONNECT target or a failure description.
func socks5Server(t *testing.T, echo net.Listener, wantUser, wantPass string, recorded chan<- string, gotCreds chan<- socks5Creds) net.Listener {
	return startServer(t, func(c net.Conn) {
		br := bufio.NewReader(c)
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(br, hdr); err != nil || hdr[0] != 5 || hdr[1] == 0 {
			recorded <- fmt.Sprintf("bad greeting %v %v", hdr, err)
			return
		}
		methods := make([]byte, hdr[1])
		if _, err := io.ReadFull(br, methods); err != nil {
			recorded <- fmt.Sprintf("short greeting: %v", err)
			return
		}
		want := byte(0)
		if wantUser != "" {
			want = 2
		}
		if !strings.ContainsRune(string(methods), rune(want)) {
			c.Write([]byte{5, 0xff})
			recorded <- fmt.Sprintf("method %d not offered in %v", want, methods)
			return
		}
		c.Write([]byte{5, want})
		if want == 2 {
			user, pass, err := readRFC1929(br)
			if err != nil {
				recorded <- fmt.Sprintf("bad auth: %v", err)
				return
			}
			gotCreds <- socks5Creds{user, pass}
			if user != wantUser || pass != wantPass {
				c.Write([]byte{1, 1})
				recorded <- "auth rejected"
				return
			}
			c.Write([]byte{1, 0})
		}
		req := make([]byte, 5)
		if _, err := io.ReadFull(br, req); err != nil || req[0] != 5 || req[1] != 1 || req[2] != 0 || req[3] != 3 {
			recorded <- fmt.Sprintf("bad request %v %v", req, err)
			return
		}
		rest := make([]byte, int(req[4])+2)
		if _, err := io.ReadFull(br, rest); err != nil {
			recorded <- fmt.Sprintf("short request: %v", err)
			return
		}
		host := string(rest[:req[4]])
		port := int(rest[req[4]])<<8 | int(rest[req[4]+1])
		recorded <- fmt.Sprintf("%s:%d", host, port)
		c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		pipeTo(c, br, echo.Addr().String())
	})
}

func readRFC1929(br *bufio.Reader) (string, string, error) {
	ver, err := br.ReadByte()
	if err != nil {
		return "", "", err
	}
	if ver != 1 {
		return "", "", fmt.Errorf("auth version %d", ver)
	}
	field := func() (string, error) {
		n, err := br.ReadByte()
		if err != nil {
			return "", err
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(br, b); err != nil {
			return "", err
		}
		return string(b), nil
	}
	user, err := field()
	if err != nil {
		return "", "", err
	}
	pass, err := field()
	if err != nil {
		return "", "", err
	}
	return user, pass, nil
}

func TestDialSOCKS5(t *testing.T) {
	echo := startEcho(t)
	recorded := make(chan string, 1)
	srv := socks5Server(t, echo, "", "", recorded, nil)

	p := mustResolve(t, "socks5://"+srv.Addr().String())
	c, err := p.DialContext(context.Background(), "tcp", "relay.tw.test:443")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	assertEcho(t, c)
	if got := <-recorded; got != "relay.tw.test:443" {
		t.Fatalf("socks server recorded %q", got)
	}
}

func TestDialSOCKS5WithCredentials(t *testing.T) {
	echo := startEcho(t)
	recorded := make(chan string, 1)
	gotCreds := make(chan socks5Creds, 1)
	srv := socks5Server(t, echo, "user", "pw", recorded, gotCreds)

	p := mustResolve(t, "socks5://user:pw@"+srv.Addr().String())
	c, err := p.DialContext(context.Background(), "tcp", "relay.tw.test:443")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	assertEcho(t, c)
	if got := <-gotCreds; got != (socks5Creds{"user", "pw"}) {
		t.Fatalf("socks server saw credentials %+v, want user/pw", got)
	}
	if got := <-recorded; got != "relay.tw.test:443" {
		t.Fatalf("socks server recorded %q", got)
	}
}

func TestDialSOCKS5WrongCredentials(t *testing.T) {
	echo := startEcho(t)
	recorded := make(chan string, 1)
	gotCreds := make(chan socks5Creds, 1)
	srv := socks5Server(t, echo, "user", "pw", recorded, gotCreds)

	p := mustResolve(t, "socks5://user:wrong@"+srv.Addr().String())
	c, err := p.DialContext(context.Background(), "tcp", "relay.tw.test:443")
	if err == nil {
		c.Close()
		t.Fatal("expected error for wrong credentials")
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Fatalf("error leaks the password: %v", err)
	}
	if got := <-gotCreds; got != (socks5Creds{"user", "wrong"}) {
		t.Fatalf("socks server saw credentials %+v", got)
	}
	if got := <-recorded; got != "auth rejected" {
		t.Fatalf("socks server recorded %q, want auth rejected", got)
	}
}

func connectServer(t *testing.T, echo net.Listener, status string, gotReq chan<- []string) net.Listener {
	return startServer(t, func(c net.Conn) {
		br := bufio.NewReader(c)
		var lines []string
		for {
			l, err := br.ReadString('\n')
			if err != nil {
				return
			}
			l = strings.TrimRight(l, "\r\n")
			if l == "" {
				break
			}
			lines = append(lines, l)
		}
		gotReq <- lines
		fmt.Fprintf(c, "HTTP/1.1 %s\r\n\r\n", status)
		if strings.HasPrefix(status, "200") {
			pipeTo(c, br, echo.Addr().String())
		}
	})
}

func TestDialHTTPConnect(t *testing.T) {
	echo := startEcho(t)
	gotReq := make(chan []string, 1)
	srv := connectServer(t, echo, "200 Connection established", gotReq)

	p := mustResolve(t, "http://u:p%40ss@"+srv.Addr().String())
	c, err := p.DialContext(context.Background(), "tcp", "relay.tw.test:443")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	assertEcho(t, c)

	lines := <-gotReq
	if lines[0] != "CONNECT relay.tw.test:443 HTTP/1.1" {
		t.Fatalf("request line = %q", lines[0])
	}
	found := false
	for _, l := range lines {
		if l == "Proxy-Authorization: Basic dTpwQHNz" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing Proxy-Authorization header in %q", lines)
	}
}

func TestDialHTTPConnectNoCredentials(t *testing.T) {
	echo := startEcho(t)
	gotReq := make(chan []string, 1)
	srv := connectServer(t, echo, "200 OK", gotReq)

	p := mustResolve(t, "http://"+srv.Addr().String())
	c, err := p.DialContext(context.Background(), "tcp", "relay.tw.test:443")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	assertEcho(t, c)
	for _, l := range <-gotReq {
		if strings.HasPrefix(l, "Proxy-Authorization") {
			t.Fatalf("unexpected header %q", l)
		}
	}
}

func TestDialHTTPConnectRejected(t *testing.T) {
	gotReq := make(chan []string, 1)
	srv := connectServer(t, nil, "407 Proxy Authentication Required", gotReq)

	p := mustResolve(t, "http://u:secretpw@"+srv.Addr().String())
	start := time.Now()
	c, err := p.DialContext(context.Background(), "tcp", "relay.tw.test:443")
	if err == nil {
		c.Close()
		t.Fatal("expected error")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	if !strings.Contains(err.Error(), "407") {
		t.Fatalf("error lacks status: %v", err)
	}
	if strings.Contains(err.Error(), "secretpw") || strings.Contains(err.Error(), "u:") {
		t.Fatalf("error leaks credentials: %v", err)
	}
}

func TestDialHTTPConnectKeepsBytesAfterReply(t *testing.T) {
	srv := startServer(t, func(c net.Conn) {
		br := bufio.NewReader(c)
		for {
			l, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if strings.TrimRight(l, "\r\n") == "" {
				break
			}
		}
		c.Write([]byte("HTTP/1.1 200 OK\r\n\r\nhello"))
		io.Copy(io.Discard, br)
	})

	p := mustResolve(t, "http://"+srv.Addr().String())
	c, err := p.DialContext(context.Background(), "tcp", "relay.tw.test:443")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, ok := c.(*bufferedConn); !ok {
		t.Fatalf("conn is %T, want *bufferedConn (bytes after the CONNECT reply were not buffered)", c)
	}
	c.SetDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 16)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := string(buf[:n]); got != "hello" {
		t.Fatalf("first read = %q, want hello", got)
	}
}

func TestDialHTTPConnectRejectsCRLF(t *testing.T) {
	var accepts atomic.Int32
	srv := startServer(t, func(c net.Conn) { accepts.Add(1) })

	p := mustResolve(t, "http://"+srv.Addr().String())
	c, err := p.DialContext(context.Background(), "tcp", "relay:443\r\nX: y")
	if err == nil {
		c.Close()
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "CR or LF") {
		t.Fatalf("err = %v, want it to mention CR or LF", err)
	}
	time.Sleep(50 * time.Millisecond)
	if n := accepts.Load(); n != 0 {
		t.Fatalf("proxy accepted %d connections, want 0", n)
	}
}

func TestDialHTTPConnectHonoursCancel(t *testing.T) {
	srv := startServer(t, func(c net.Conn) { io.Copy(io.Discard, c) })

	p := mustResolve(t, "http://"+srv.Addr().String())
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	c, err := p.DialContext(ctx, "tcp", "relay.tw.test:443")
	if err == nil {
		c.Close()
		t.Fatal("expected error")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("DialContext returned after %v, want < 2s", d)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want it to wrap context.Canceled", err)
	}
}
