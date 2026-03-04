# Performance

Tunnel Whisperer adds encryption and relay-routing overhead to every connection. This page documents measured performance characteristics and explains how to reproduce the benchmarks.

## Test Environment

| Component | Details |
|-----------|---------|
| **Server** | Windows, PostgreSQL 16.11 |
| **Client** | Windows (WSL2 for curl) |
| **Relay** | Hetzner cloud VM (Ubuntu), Caddy + Xray |
| **Network RTT** | ~30ms (client to relay, measured via TCP handshake) |
| **TW version** | v1.5.x |
| **Transport** | VLESS + XHTTP + TLS 1.3 on port 443 |

---

## Throughput (File Transfer)

A 100 MB file transferred via HTTP, measured with `curl`. This tests sustained bulk throughput — the most relevant metric for data-heavy workloads like database replication, file sync, and backups.

| Method | Speed | Time (100 MB) | Throughput |
|--------|------:|-----:|-----------|
| Local (loopback) | 1,118 MB/s | 0.09s | ~8.9 Gbps |
| Direct SSH (`ssh -L`) | 112 MB/s | 0.93s | ~900 Mbps |
| **Tunnel Whisperer** | **21 MB/s** | **5.0s** | **~168 Mbps** |

### Reproduce It

**1. Create a test file on the server:**

```powershell
# Windows
fsutil file createnew C:\temp\testfile.bin 104857600

# Linux
dd if=/dev/zero of=/tmp/testfile.bin bs=1M count=100
```

**2. Serve it with a simple HTTP server:**

```bash
python -m http.server 8080 --directory /tmp    # Linux
python -m http.server 8080 --directory C:\temp  # Windows
```

**3. Map port 8080 through TW** (add a port mapping for the test user).

**4. Download and measure:**

```bash
# Through Tunnel Whisperer
curl -o /dev/null -w "speed: %{speed_download} bytes/sec\ntime: %{time_total}s\n" \
  http://localhost:<TW_PORT>/testfile.bin

# Through direct SSH (for comparison)
# First: ssh -L 8080:localhost:8080 user@server
curl -o /dev/null -w "speed: %{speed_download} bytes/sec\ntime: %{time_total}s\n" \
  http://localhost:8080/testfile.bin

# Local baseline (run on the server itself)
curl -o /dev/null -w "speed: %{speed_download} bytes/sec\ntime: %{time_total}s\n" \
  http://localhost:8080/testfile.bin
```

---

## Latency (PostgreSQL pgbench)

`pgbench` with 50 concurrent clients, 5 threads, 60-second run. This is a latency-sensitive, synchronous workload — it amplifies round-trip time into a throughput metric since each client waits for a response before sending the next query.

| Method | TPS | Avg Latency | Failed |
|--------|----:|------:|-------:|
| Local (loopback) | ~7,855 | ~6 ms | 0 |
| Direct SSH (`ssh -L`) | ~1,790 | ~28 ms | 0 |
| **Tunnel Whisperer** | **~80–95** | **~530–630 ms** | **0** |

### Reproduce It

**1. Create and initialize the test database:**

```bash
createdb -U postgres pgbench
pgbench -i -s 10 -U postgres pgbench
```

**2. Run the benchmark:**

```bash
pgbench -c 50 -j 5 -T 60 -U postgres pgbench
```

Run this three ways: locally on the server, through `ssh -L localhost:5432:localhost:5432`, and through TW with port 5432 mapped.

---

## Understanding the Overhead

### Where the time goes

```
Client app
  → SSH encryption (in-process)
    → Xray VLESS encode (in-process)
      → TLS 1.3 to relay (~30ms network hop)
        → Caddy TLS termination
          → Xray decode + freedom outbound
            → SSH reverse tunnel to server (~30ms network hop)
              → Server app
```

Each request-response adds **two relay hops** (~60ms minimum). The XHTTP transport splits data into HTTP requests, adding per-chunk framing overhead on top of the network latency.

### Latency vs throughput

Tunnel Whisperer is optimized for **throughput over restrictive networks**, not for minimizing latency. The design prioritizes:

- Traversing firewalls and DPI (looks like normal HTTPS)
- Surviving aggressive connection timeouts (XHTTP splits long-lived streams)
- Zero-trust relay operation (relay never sees plaintext)

For latency-sensitive workloads, the overhead is dominated by network round-trip time to the relay. Placing the relay geographically closer to both endpoints reduces this proportionally.

### Workload characteristics

| Workload type | Expected performance | Why |
|---------------|---------------------|-----|
| Bulk transfer (backup, sync) | Good (~168 Mbps) | Throughput-bound, amortizes latency |
| Streaming (logs, monitoring) | Good | Continuous flow, latency less visible |
| Interactive (SSH terminal) | Good | Human typing speed hides tunnel latency |
| Database (connection pooled) | Good | Pipelining reduces per-query impact |
| Database (synchronous) | Latency-limited | Each query pays full round-trip cost |
| High-frequency RPC | Latency-limited | Many small round-trips amplify overhead |

!!! tip "Connection pooling helps"
    For database workloads, use connection pooling (e.g. PgBouncer) to batch queries and reduce the number of round-trips. This significantly improves effective throughput through the tunnel.
