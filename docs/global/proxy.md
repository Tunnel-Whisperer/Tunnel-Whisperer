# Proxy Configuration

If a machine is behind a corporate firewall that requires outbound connections to go through a proxy, Tunnel Whisperer can route its traffic through a SOCKS5 or HTTP proxy. The setting works in **all three roles**, and one resolved proxy is used for every outbound connection tw makes: the tunnel, enrollment, cloud API checks, relay checks, direct relay SSH and Terraform.

## Supported Protocols

- `socks5://[user:pass@]host:port`
- `http://[user:pass@]host:port`

If the port is omitted, 1080 is used (for both `socks5://` and `http://`).

`socks5h://` is accepted as an alias of `socks5://`; the proxy always resolves the relay's DNS name, so the two behave identically.

## Precedence

1. `proxy:` in `config.yaml` (set with `tw proxy set`). If set, it is the proxy for all tw traffic and the environment proxy variables are ignored entirely.
2. Otherwise the environment, first non-empty wins: `HTTPS_PROXY`, `https_proxy`, `ALL_PROXY`, `all_proxy`.
3. Otherwise connections are direct.

An unsupported scheme in either source is an error, never a silent fallback to direct.

## Environment variables

- `HTTPS_PROXY`, `https_proxy`, `ALL_PROXY`, `all_proxy` are read, in that order.
- A value without a scheme (`proxy.corp:3128`) is treated as `http://`; a missing port means 1080.
- `HTTP_PROXY` is ignored, because every tw connection is TLS or SSH, never plain HTTP.
- `NO_PROXY` / `no_proxy` is honoured only when the proxy comes from the environment. A configured proxy applies to all hosts. Loopback targets are always direct.
- systemd and launchd services usually do not inherit your shell environment. For a daemon, `tw proxy set` is the reliable choice.

## Setting a Proxy

### CLI

```bash
tw proxy set socks5://proxy.corp.example.com:1080
tw proxy set http://user:pass@proxy.corp.example.com:8080
```

### Dashboard

Go to **Config** → **Proxy** → enter the URL → **Save**.

## Clearing a Proxy

### CLI

```bash
tw proxy clear
```

### Dashboard

Go to **Config** → **Proxy** → **Clear**.

## Viewing the Current Proxy

```bash
tw proxy
```

```text
  Proxy: socks5://proxy.corp.example.com:1080 (source: config)
```

When the proxy comes from the environment, a second line follows: `(resolved from this shell's environment; a service may differ — check the daemon log line 'outbound proxy')`.

The source says where the proxy came from: `config`, `env <NAME>` (for example `env HTTPS_PROXY`), or `none`. Credentials are always shown as `redacted`. If a proxy is configured while a proxy environment variable is also set, `tw proxy` adds a line such as `HTTPS_PROXY is set but ignored while a proxy is configured`.

## What Is Proxied

| Traffic | Proxied |
|---|---|
| Xray tunnel (VLESS/XHTTP to relay:443) | yes |
| Enrollment (`tw join`) | yes |
| Cloud token check (Hetzner / DigitalOcean) | yes |
| Relay HTTPS checks | yes |
| Direct relay SSH (port 22) | yes |
| Terraform | yes |

## When It Takes Effect

Proxy changes are saved to `config.yaml` and take effect on the next server start or client reconnect. If a server or client is currently running, the dashboard shows a "Configuration has changed" notification prompting a restart/reconnect.

## How It Works

For the tunnel, the proxy is applied to Xray's outbound transport settings (a `proxy-out` outbound). All VLESS+XHTTP+TLS traffic to the relay is routed through the specified proxy, which sees only encrypted TLS traffic to your relay domain on port 443.

Everything else uses the same resolved proxy on the Go side: HTTP clients use it as their transport proxy, and relay SSH connects through a SOCKS5 or HTTP CONNECT dialer. Terraform receives `HTTPS_PROXY` and `HTTP_PROXY` in its environment when the proxy is set in config; an environment proxy is inherited as is.
