# Installation

## Build from Source

Requires **Go 1.25+**.

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

=== "macOS"

    ```bash
    make build-darwin
    # or manually:
    GOOS=darwin GOARCH=amd64 go build -o bin/tw-darwin ./cmd/tw
    ```

=== "All"

    ```bash
    make build-all
    ```

### Makefile Targets

| Target | Description |
| ------ | ----------- |
| `make build` | Build for current OS |
| `make build-linux` | Cross-compile for Linux amd64 |
| `make build-windows` | Cross-compile for Windows amd64 |
| `make build-darwin` | Cross-compile for macOS amd64 |
| `make build-all` | Build Linux, Windows, and macOS |
| `make run` | Build and run locally |
| `make clean` | Remove build artifacts |

### Version Injection

All build targets inject the version at build time via `-ldflags`. The version is auto-detected from the latest git tag:

```bash
make build                    # version from git describe
make build VERSION=v1.2.3     # explicit override
```

Release builds (GitHub Actions) inject the exact git tag (e.g. `v1.2.3`) into the binary.

## Verify

```bash
tw --version    # prints: tw version v1.2.3
tw --help
```

## Install as a System Service

After building, you can register `tw` as a system service so it starts on boot and runs in the background.

=== "Linux (systemd)"

    **1. Copy the binary to a system path:**

    ```bash
    sudo cp bin/tw /usr/local/bin/tw
    sudo chmod +x /usr/local/bin/tw
    ```

    **2. Install and start the service:**

    ```bash
    sudo tw service install
    sudo tw service start
    ```

    This creates a systemd unit at `/etc/systemd/system/tw.service` that runs `tw dashboard` with automatic restart on failure.

    **3. Manage the service:**

    ```bash
    sudo tw service stop        # stop the service
    sudo tw service uninstall   # remove the service
    sudo systemctl status tw    # check service status
    sudo journalctl -u tw -f   # follow service logs
    ```

=== "Windows (SCM)"

    **1. Place the binary in a permanent location:**

    ```powershell
    mkdir C:\tw
    copy bin\tw.exe C:\tw\tw.exe
    ```

    **2. Install and start the service (run as Administrator):**

    ```powershell
    C:\tw\tw.exe service install
    C:\tw\tw.exe service start
    ```

    This registers a Windows service that starts automatically on boot.

    **3. Manage the service:**

    ```powershell
    tw.exe service stop         # stop the service
    tw.exe service uninstall    # remove the service
    ```

    You can also manage it from the Services console (`services.msc`) — look for **Tunnel Whisperer**.

=== "macOS (launchd)"

    **1. Copy the binary to a system path:**

    ```bash
    sudo cp bin/tw-darwin /usr/local/bin/tw
    sudo chmod +x /usr/local/bin/tw
    ```

    **2. Allow the binary in macOS security settings:**

    On first run, macOS Gatekeeper will block the unsigned binary. To allow it:

    1. Open **System Settings > Privacy & Security**
    2. Scroll to the **Security** section — you will see a message about `tw` being blocked
    3. Click **Allow Anyway**
    4. Run `tw` again and click **Open** in the confirmation dialog

    Alternatively, remove the quarantine attribute directly:

    ```bash
    sudo xattr -d com.apple.quarantine /usr/local/bin/tw
    ```

    **3. Install and start the service:**

    ```bash
    sudo tw service install
    sudo tw service start
    ```

    This creates a launchd plist at `/Library/LaunchDaemons/com.tunnelwhisperer.tw.plist` that keeps the service running and starts it on boot.

    **4. Manage the service:**

    ```bash
    sudo tw service stop        # stop the service
    sudo tw service uninstall   # remove the service
    sudo launchctl list | grep tw   # check if service is loaded
    cat /var/log/tw.log         # view service logs
    cat /var/log/tw.err.log     # view error logs
    ```

The service runs `tw dashboard`, which auto-starts the server or client based on your config mode. See [CLI Reference — Running as a Service](../reference/cli.md#running-as-a-service) for details.

## Config Directory

Tunnel Whisperer stores configuration in a platform-specific directory:

| Platform | Path |
| -------- | ---- |
| Linux | `/etc/tw/config/` |
| macOS | `/etc/tw/config/` |
| Windows | `C:\ProgramData\tw\config\` |

Override with the `TW_CONFIG_DIR` environment variable.
