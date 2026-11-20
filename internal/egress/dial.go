package egress

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	xproxy "golang.org/x/net/proxy"
)

const connectTimeout = 30 * time.Second

// HTTPTransport returns a transport whose Proxy field is always set
// explicitly (a func, or nil when there is no proxy) so Go's environment
// defaults can never apply.
func (p Proxy) HTTPTransport(tlsCfg *tls.Config) *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.TLSClientConfig = tlsCfg
	if p.Source != SourceNone {
		tr.Proxy = func(r *http.Request) (*url.URL, error) {
			return p.ForHost(r.URL.Host), nil
		}
	}
	return tr
}

// HTTPClient returns an http.Client using HTTPTransport.
func (p Proxy) HTTPClient(timeout time.Duration, tlsCfg *tls.Config) *http.Client {
	return &http.Client{Timeout: timeout, Transport: p.HTTPTransport(tlsCfg)}
}

// DialContext connects to addr directly or through the proxy, as ForHost decides.
func (p Proxy) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	u := p.ForHost(addr)
	if u == nil {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	switch u.Scheme {
	case "socks5":
		return dialSOCKS5(ctx, u, network, addr)
	case "http":
		return dialHTTPConnect(ctx, u, network, addr)
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
	}
}

func dialSOCKS5(ctx context.Context, u *url.URL, network, addr string) (net.Conn, error) {
	var auth *xproxy.Auth
	if u.User != nil {
		pw, _ := u.User.Password()
		auth = &xproxy.Auth{User: u.User.Username(), Password: pw}
	}
	d, err := xproxy.SOCKS5("tcp", u.Host, auth, &net.Dialer{})
	if err != nil {
		return nil, fmt.Errorf("socks5 proxy %s: %w", u.Host, err)
	}
	cd, ok := d.(xproxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("socks5 proxy %s: dialer does not support contexts", u.Host)
	}
	conn, err := cd.DialContext(ctx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("socks5 proxy %s: connect %s: %w", u.Host, addr, err)
	}
	return conn, nil
}

func dialHTTPConnect(ctx context.Context, u *url.URL, network, addr string) (net.Conn, error) {
	if strings.ContainsAny(addr, "\r\n") {
		return nil, fmt.Errorf("http proxy %s: refusing CONNECT target containing CR or LF", u.Host)
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, network, u.Host)
	if err != nil {
		return nil, fmt.Errorf("http proxy %s: %w", u.Host, err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	c, err := connectHandshake(ctx, conn, u, addr)
	if err != nil {
		stop()
		conn.Close()
		if ctx.Err() != nil {
			return nil, fmt.Errorf("http proxy %s: CONNECT %s: %w", u.Host, addr, ctx.Err())
		}
		return nil, err
	}
	if !stop() {
		conn.Close()
		return nil, fmt.Errorf("http proxy %s: CONNECT %s: %w", u.Host, addr, ctx.Err())
	}
	return c, nil
}

// connectHandshake runs the CONNECT exchange on conn. The caller closes conn
// on error.
func connectHandshake(ctx context.Context, conn net.Conn, u *url.URL, addr string) (net.Conn, error) {
	deadline := time.Now().Add(connectTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("http proxy %s: %w", u.Host, err)
	}

	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", addr, addr)
	if u.User != nil {
		pw, _ := u.User.Password()
		cred := base64.StdEncoding.EncodeToString([]byte(u.User.Username() + ":" + pw))
		req += "Proxy-Authorization: Basic " + cred + "\r\n"
	}
	req += "\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		return nil, fmt.Errorf("http proxy %s: write CONNECT %s: %w", u.Host, addr, err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return nil, fmt.Errorf("http proxy %s: read CONNECT %s reply: %w", u.Host, addr, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http proxy %s refused CONNECT %s: %s", u.Host, addr, resp.Status)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, fmt.Errorf("http proxy %s: %w", u.Host, err)
	}
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, r: br}, nil
	}
	return conn, nil
}

// bufferedConn reads through br so bytes the proxy sent right after its CONNECT
// reply are not lost.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) { return c.r.Read(b) }
