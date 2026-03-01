# Installation

## Build from Source

Requires **Go 1.22+**.

```bash
go build -o bin/tw ./cmd/tw
```

### Cross-Compile

=== "Linux"

    ```bash
    make build-linux
    # or manually:
    GOOS=linux GOARCH=amd64 go build -o bin/tw ./cmd/tw
    ```

=== "Windows"

    ```bash
    make build-windows
    # or manually:
    GOOS=windows GOARCH=amd64 go build -o bin/tw.exe ./cmd/tw
    ```

=== "Both"

    ```bash
    make build-all
    ```

### Makefile Targets

| Target | Description |
| ------ | ----------- |
| `make build` | Build for current OS |
| `make build-linux` | Cross-compile for Linux amd64 |
| `make build-windows` | Cross-compile for Windows amd64 |
| `make build-all` | Build both Linux and Windows |
| `make run` | Build and run locally |
| `make clean` | Remove build artifacts |

## Verify

```bash
tw --help
```

## Install as a System Service

After building, you can register `tw` as a system service so it starts on boot and runs in the background.

=== "Linux (systemd)"

    ```bash
    sudo tw service install
    sudo tw service start
    ```

    This creates a systemd unit at `/etc/systemd/system/tw.service` that runs `tw dashboard` with automatic restart on failure.

=== "Windows (SCM)"

    ```powershell
    tw.exe service install
    tw.exe service start
    ```

    This registers a Windows service that starts automatically on boot. Manage it from `services.msc` or with `tw.exe service stop` / `tw.exe service uninstall`.

The service runs `tw dashboard`, which auto-starts the server or client based on your config mode. See [CLI Reference — Running as a Service](../reference/cli.md#running-as-a-service) for details.

## Config Directory

Tunnel Whisperer stores configuration in a platform-specific directory:

| Platform | Path |
| -------- | ---- |
| Linux | `/etc/tw/config/` |
| Windows | `C:\ProgramData\tw\config\` |

Override with the `TW_CONFIG_DIR` environment variable.
