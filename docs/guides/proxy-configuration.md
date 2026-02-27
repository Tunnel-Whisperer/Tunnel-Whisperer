# Proxy Configuration

If your server or client is behind a corporate firewall that requires outbound connections to go through a proxy, Tunnel Whisperer can route its Xray tunnel through a SOCKS5 or HTTP proxy.

## Supported Protocols

- `socks5://[user:pass@]host:port`
- `http://[user:pass@]host:port`

## Setting a Proxy

### CLI

```bash
tw proxy set socks5://proxy.corp.example.com:1080
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

## Viewing Current Proxy

```bash
tw proxy
```

## When It Takes Effect

Proxy changes are saved to `config.yaml` and take effect on the next server start or client reconnect. If a server or client is currently running, the dashboard shows a "Configuration has changed" notification prompting a restart/reconnect.

## How It Works

The proxy is applied to Xray's outbound transport settings. All VLESS+splitHTTP+TLS traffic to the relay is routed through the specified proxy. The proxy sees only encrypted TLS traffic to your relay domain on port 443.
