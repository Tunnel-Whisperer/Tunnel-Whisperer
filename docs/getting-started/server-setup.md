# Server Setup

This guide covers setting up the server side — provisioning a relay, creating users, and starting the server.

## 1. Start the Dashboard

The easiest way to manage everything is through the web dashboard:

```bash
tw dashboard
```

This opens a browser UI on `http://localhost:8080` where you can manage the relay, users, and server from a single page. On first run, choose **Server** mode.

Alternatively, use the CLI for each step below.

## 2. Provision a Relay

The relay is a lightweight cloud VM that both server and clients connect to. Provision one with the interactive wizard:

```bash
tw create relay-server
```

This 8-step wizard:

1. Generates SSH keys (ed25519)
2. Generates an Xray UUID
3. Asks for your relay domain (e.g. `relay.example.com`)
4. Selects cloud provider (Hetzner, DigitalOcean, or AWS)
5. Enters and validates credentials
6. Runs Terraform to provision the VM
7. Waits for DNS resolution and TLS certificate issuance

The relay VM is configured with Caddy (TLS), Xray (VLESS transport), and a locked-down firewall (only ports 80 and 443).

!!! tip "Manual setup"
    If you prefer to set up the relay on your own VPS, use the dashboard's **Manual** option or `tw relay generate-script` to get a bash install script.

For details, see the [Relay Provisioning Guide](../guides/relay-provisioning.md).

## 3. Create Users

Each client needs a user account with restricted port access:

```bash
tw create user
```

The wizard asks for a username and port mappings (which server ports the client can access). It generates SSH keys, registers the user's UUID on the relay, and creates a config bundle.

Send the generated config files (or zip) to the client operator.

For details, see the [User Management Guide](../guides/user-management.md).

## 4. Start the Server

```bash
tw serve
```

This starts:

1. **Embedded SSH server** on `:2222` with dynamic `authorized_keys` and per-user `permitopen` restrictions
2. **Xray tunnel** to the relay (VLESS + splitHTTP + TLS on port 443)
3. **SSH reverse tunnel** through Xray, exposing the server's SSH on the relay
4. **gRPC API** on `:50051`

!!! note "Auto-start"
    When using `tw dashboard`, the server starts automatically if a relay is provisioned.

## What's Next

- [Create more users](../guides/user-management.md)
- [Configure a proxy](../guides/proxy-configuration.md) if your server is behind a corporate firewall
- [Test the relay](../guides/troubleshooting.md) with `tw test relay`
