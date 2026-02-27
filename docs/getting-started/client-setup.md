# Client Setup

This guide covers connecting as a client after receiving a config bundle from the server operator.

## 1. Receive Config Bundle

The server admin will provide you with a zip file containing:

- `config.yaml` — client configuration (relay address, tunnel mappings, SSH user)
- `id_ed25519` — your SSH private key
- `id_ed25519.pub` — your SSH public key

## 2. Upload Config

### Via Dashboard (Recommended)

```bash
tw dashboard
```

Choose **Client** mode on first run. Then drag-and-drop the zip file into the upload area, or click to browse.

### Via CLI

Extract the zip contents into the config directory:

=== "Linux"

    ```bash
    unzip client-config.zip -d /etc/tw/config/
    ```

=== "Windows"

    Extract to `C:\ProgramData\tw\config\`

## 3. Connect

### Via Dashboard

Click **Connect** on the client status page. The dashboard shows real-time progress as the Xray tunnel and SSH port forwarding start.

### Via CLI

```bash
tw connect
```

This starts:

1. **Xray client tunnel** to the relay (VLESS + splitHTTP + TLS)
2. **SSH connection** through Xray to the server (public key auth)
3. **Local port listeners** for all configured tunnel mappings

## 4. Verify

Test the tunnel by connecting to your mapped local ports. For example, if PostgreSQL is mapped:

```bash
psql -h localhost -p 5432 -U myuser mydb
```

The connection goes through the tunnel transparently.

## Reconnecting

If the server admin updates your configuration, the dashboard shows a "Configuration has changed. Reconnect to apply." notification. Click **Reconnect** to apply changes without a full restart.

## Auto-Reconnection

The client automatically reconnects with exponential backoff if the connection drops:

- 2s for the first 8 attempts
- 4s, then 8s, then 16s, then 30s max
- Successful connection resets the backoff immediately
