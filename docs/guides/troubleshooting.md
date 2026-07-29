# Troubleshooting

## Checking Status

### CLI

```bash
tw status
```

Shows the current mode, relay info, and server/client state. If a daemon is running, it connects via gRPC to get live status.

### Dashboard

The main page shows real-time status for all components with color-coded indicators:

- **Green** (up) — component is healthy
- **Red** (down/error) — component has failed
- **Yellow** — transitional state (starting/stopping)

## Testing the Relay

```bash
tw relay test
```

Runs a 3-step diagnostic:

1. **DNS resolution** — verifies the domain resolves correctly
2. **HTTPS/Caddy** — confirms TLS certificate is valid and Caddy responds
3. **Xray + SSH** — establishes a full tunnel and opens an SSH session

## Log Levels

Increase verbosity for debugging:

### CLI

```bash
tw --log-level debug server start
```

### Dashboard

Go to **Config** → **Log Level** → select **debug** → **Save**. Restart/reconnect to apply.

The log level is persisted to `config.yaml`. When set via the CLI `--log-level` flag, it also updates the config for dashboard consistency.

### Console Logs

The dashboard shows real-time logs at the bottom of the main page. Click **Clear** to reset the log view.

## Common Issues

### DNS Not Resolving

After provisioning a relay, you need to create a DNS A record pointing your domain to the relay's IP address. The provisioning wizard will wait and retry until DNS resolves.

**Fix:** Create the A record with your DNS provider. Allow up to 5 minutes for propagation.

### TLS Certificate Not Ready

Caddy automatically provisions a TLS certificate via Let's Encrypt. This requires:

- DNS must resolve to the relay IP
- Port 80 must be accessible (for ACME challenge)

**Fix:** Ensure the DNS record is correct and the relay firewall allows port 80.

### Tunnel Drops and Reconnects

The client and server automatically reconnect with exponential backoff (2s → 4s → 8s → 16s → 30s max). Frequent reconnects may indicate:

- Unstable network connection
- Proxy or firewall dropping idle connections
- Relay VM resource constraints

**Fix:** Check the debug logs for specific error messages. Ensure keepalive traffic can pass through any intermediate proxies.

### Mode Enforcement Errors

```
this command requires server mode, but tw is configured in client mode
```

Server-only commands (like `tw server user create`) cannot run in client mode, and vice versa.

**Fix:** Ensure you're running the command on the correct machine, or check `mode` in your `config.yaml`.

### Config Changed Notification

The dashboard shows "Configuration has changed. Restart/Reconnect to apply." when the config file on disk differs from what was loaded at startup.

**Fix:** Click the Restart (server) or Reconnect (client) button to apply changes.

### "VLESS (with no Flow, etc.) is deprecated" Warning on the Relay

Relays provisioned by an older `tw` (which pinned Xray v26.2.6) may show this in the Xray logs (`journalctl -u xray`) at startup — newer Xray versions (the current pin is v26.6.27) no longer print it:

```
[Warning] common/errors: The feature VLESS (with no Flow, etc.) is deprecated, not recommended for using and might be removed. Please migrate to VLESS with Flow & Seed as soon as possible.
```

This is **normal and harmless**. It is an upstream xray-core advisory aimed at setups where VLESS is the only encryption layer. Tunnel Whisperer intentionally runs "plain" VLESS:

- **Flow** (XTLS Vision) requires a raw TCP+TLS transport. Tunnel Whisperer runs VLESS over XHTTP behind Caddy, where Flow does not apply by design.
- **Seed** (VLESS Encryption) would add encryption at the VLESS layer, but the stream is already wrapped in Caddy's TLS 1.3 with mutual TLS, and the payload inside is end-to-end SSH-encrypted. A third encryption layer adds nothing.

Upstream explicitly keeps plain VLESS working (it is their "non-removal" deprecation class), so no configuration change is needed.

**Fix:** None required — safe to ignore.
