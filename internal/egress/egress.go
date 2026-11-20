// Package egress decides which outbound proxy, if any, applies to tw's
// connections. Precedence: config "proxy:" first, then the environment
// (HTTPS_PROXY, https_proxy, ALL_PROXY, all_proxy; first non-empty wins),
// otherwise direct. HTTP_PROXY is never read; NO_PROXY applies to env only.
package egress

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"golang.org/x/net/http/httpproxy"
)

const defaultPort = "1080"

// Source says where the resolved proxy came from.
type Source string

const (
	SourceNone   Source = "none"
	SourceConfig Source = "config"
	SourceEnv    Source = "env"
)

var envVars = []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"}

// Proxy is the resolved outbound proxy.
type Proxy struct {
	URL     *url.URL // nil iff Source == SourceNone
	Source  Source
	EnvVar  string // e.g. "HTTPS_PROXY"; set iff Source == SourceEnv
	noProxy string // NO_PROXY/no_proxy value; set iff Source == SourceEnv

	proxyFunc func(*url.URL) (*url.URL, error) // built once by Resolve
}

// ParseURL validates a proxy URL (http or socks5, host required) and fills in
// the default port 1080 when none is given. socks5h is accepted as an alias of
// socks5 and normalised to it.
func ParseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		if strings.Contains(raw, "@") {
			return nil, errors.New("invalid proxy URL (percent-encode special characters in the user/password)")
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("invalid proxy URL: %v", err)
	}
	if u.Scheme == "socks5h" {
		u.Scheme = "socks5"
	}
	if u.Scheme != "http" && u.Scheme != "socks5" {
		return nil, fmt.Errorf("unsupported proxy scheme %q (use socks5:// or http://)", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("proxy URL must include a host")
	}
	if u.Port() == "" {
		u.Host = net.JoinHostPort(u.Hostname(), defaultPort)
	}
	return u, nil
}

// Resolve picks the proxy: cfgProxy if set, else the first non-empty proxy
// environment variable (via lookup), else none.
func Resolve(cfgProxy string, lookup func(string) (string, bool)) (Proxy, error) {
	if cfgProxy != "" {
		u, err := ParseURL(cfgProxy)
		if err != nil {
			return Proxy{}, fmt.Errorf("config proxy: %w", err)
		}
		p := Proxy{URL: u, Source: SourceConfig}
		p.proxyFunc = p.buildProxyFunc()
		return p, nil
	}
	for _, name := range envVars {
		v, ok := lookup(name)
		if !ok || v == "" {
			continue
		}
		if !strings.Contains(v, "://") {
			v = "http://" + v
		}
		u, err := ParseURL(v)
		if err != nil {
			return Proxy{}, fmt.Errorf("%s: %w", name, err)
		}
		p := Proxy{URL: u, Source: SourceEnv, EnvVar: name}
		for _, np := range []string{"NO_PROXY", "no_proxy"} {
			if nv, ok := lookup(np); ok && nv != "" {
				p.noProxy = nv
				break
			}
		}
		p.proxyFunc = p.buildProxyFunc()
		return p, nil
	}
	return Proxy{Source: SourceNone}, nil
}

func (p Proxy) buildProxyFunc() func(*url.URL) (*url.URL, error) {
	s := p.URL.String()
	cfg := httpproxy.Config{HTTPProxy: s, HTTPSProxy: s, NoProxy: p.noProxy}
	return cfg.ProxyFunc()
}

// RedactURL returns u as a string with any userinfo replaced by "redacted".
func RedactURL(u *url.URL) string {
	r := *u
	if r.User != nil {
		r.User = url.User("redacted")
	}
	return r.String()
}

// String describes the proxy with credentials redacted.
func (p Proxy) String() string {
	if p.Source == SourceNone || p.URL == nil {
		return "none"
	}
	if p.Source == SourceEnv {
		return fmt.Sprintf("%s (source: env %s)", RedactURL(p.URL), p.EnvVar)
	}
	return fmt.Sprintf("%s (source: %s)", RedactURL(p.URL), p.Source)
}

// URLString returns the unredacted proxy URL, or "" when there is none.
func (p Proxy) URLString() string {
	if p.URL == nil {
		return ""
	}
	return p.URL.String()
}

// ForHost returns the proxy to use for hostport ("host:port" or bare "host"),
// or nil for a direct connection. Loopback targets are always direct; NO_PROXY
// applies only to an env-sourced proxy.
func (p Proxy) ForHost(hostport string) *url.URL {
	if p.Source == SourceNone || p.URL == nil {
		return nil
	}
	fn := p.proxyFunc
	if fn == nil {
		fn = p.buildProxyFunc()
	}
	u, err := fn(&url.URL{Scheme: "https", Host: hostport})
	if err != nil || u == nil {
		return nil
	}
	// httpproxy hands back its cached pointer; copy so callers cannot mutate it.
	c := *u
	return &c
}

// URLFor returns the unredacted proxy URL to use for hostport (ForHost
// semantics), or "" when the connection is direct.
func (p Proxy) URLFor(hostport string) string {
	u := p.ForHost(hostport)
	if u == nil {
		return ""
	}
	return u.String()
}

// SubprocessEnv returns the proxy environment to inject into child processes.
// A config-sourced proxy is injected and any inherited NO_PROXY is blanked so
// the environment stays ignored entirely. An env-sourced proxy is inherited,
// except ALL_PROXY, which Go programs (Terraform) never read.
func (p Proxy) SubprocessEnv() []string {
	switch p.Source {
	case SourceConfig:
		s := p.URL.String()
		return []string{"HTTPS_PROXY=" + s, "HTTP_PROXY=" + s, "NO_PROXY=", "no_proxy="}
	case SourceEnv:
		if p.EnvVar == "ALL_PROXY" || p.EnvVar == "all_proxy" {
			s := p.URL.String()
			return []string{"HTTPS_PROXY=" + s, "HTTP_PROXY=" + s}
		}
	}
	return nil
}
