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
tw test relay
```

Runs a 3-step diagnostic:

1. **DNS resolution** — verifies the domain resolves correctly
2. **HTTPS/Caddy** — confirms TLS certificate is valid and Caddy responds
3. **Xray + SSH** — establishes a full tunnel and opens an SSH session

## Log Levels

Increase verbosity for debugging:

### CLI

```bash
tw --log-level debug serve
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
Error: this is a server command, but tw is configured in client mode
```

Server-only commands (like `tw create user`) cannot run in client mode, and vice versa.

**Fix:** Ensure you're running the command on the correct machine, or check `mode` in your `config.yaml`.

### Config Changed Notification

The dashboard shows "Configuration has changed. Restart/Reconnect to apply." when the config file on disk differs from what was loaded at startup.

**Fix:** Click the Restart (server) or Reconnect (client) button to apply changes.
