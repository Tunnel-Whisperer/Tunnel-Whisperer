# Performance

How much does Tunnel Whisperer cost compared with the two things people use
instead — a plain SSH connection, and SSH over a WireGuard mesh? This page is
generated from a reproducible benchmark (`make bench`) on the same Docker
Compose topology the end-to-end suite uses: a real relay provisioned by the
tw install script, a real enrolled server, a real client tunnel.

!!! abstract "One-line takeaway"
    On a LAN, tw moves 0.97 Gbit/s single-stream against 3.64 (Pure SSH) and 1.20 (WireGuard) Gbit/s; scp over the same LAN moves 122 MB/s against 359 and 149 MB/s; per busy CPU core tw sustains 0.12 Gbit/s against 1.37 and 0.29. On a 30 ms / 0.1 % loss WAN link every single-stream transfer is loss-bound at the same ≈0.03 Gbit/s / 2–4 MB/s; only multi-stream separates them, and there tw does not scale.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="../assets/bench-chart-dark.svg">
  <img alt="Benchmark: pure SSH vs SSH over WireGuard vs SSH over Tunnel Whisperer — LAN throughput, file copy, latency, lossy-WAN parallel streams, CPU per copy" src="../assets/bench-chart.svg" width="100%">
</picture>

## What is compared

| Transport | Path |
|-----------|------|
| **Pure SSH** | `client → sshd on server`, directly over the network (the ceiling) |
| **SSH over WireGuard** | kernel WireGuard, one direct peer-to-peer link (mesh-style, no relay) |
| **SSH over Tunnel Whisperer** | `client → tw client connect → VLESS/XHTTP/mTLS :443 → relay (Caddy client_auth → Xray) → reverse SSH → tw serve → sshd` |

Instruments, every one moving bytes server → client:

- **iperf3**, TCP, 1 and 4 streams, 10 s — the transport's own ceiling, no OpenSSH in the way.
- **netperf TCP_RR**, 64-byte request/response, 10 s — transactions per second and p50/p99 latency, the cost every database query or RPC pays. (`ping` cannot cross tw, which forwards ports, not packets; TCP_RR is the port-level equivalent.)
- **scp of a 5 GiB random file** to `/dev/null` — the real-world number. Through tw this is SSH inside tw's own end-to-end SSH inside TLS.

Two network conditions: **LAN** (the Compose bridge, ~0 RTT) isolates encryption and framing cost. **WAN** adds `tc netem` delay and loss on the client's and server's egress only — 15 ms delay and 0.1 % loss per leg, so every transport sees the same 30 ms RTT and 0.1 % loss end to end. The relay carries no netem of its own; for tw specifically that means the relay is modelled **mid-path**, splitting that same 30 ms budget into two 15 ms legs instead of adding a hop's worth of extra latency. Any advantage split TCP might show under loss is therefore a topology effect of where the relay sits, not a product win — the WAN results below are read with that in mind.

NIC offloads (`tso`/`gso`/`gro`) are disabled on the client, server and relay containers for the whole run, and `gso_max_segs 1` is set on all three. Linux TCP still builds multi-segment GSO skbs even with the offloads off (`sk_setup_caps` forces GSO for TCP), and netem drops per skb, not per wire packet: without capping segments to 1, the direct path was measured at roughly 6× fewer loss events per byte than WireGuard on this harness before the cap was applied — well short of the ≈40× theoretical bound for full 64 KiB super-packets — and tw's wire bytes were under-counted at the relay hop. With the cap in place, netem's per-packet loss lands on real 1500-byte packets for every transport equally. One consequence: all of the LAN throughput ceilings below, including Pure SSH's 3.64 Gbit/s, are software-segmentation ceilings of the Docker bridge, not product or NIC numbers. The ordering within this run is meaningful — the absolute LAN values are not: the segmentation cap costs the plain-TCP transports (Pure SSH, and tw's outer TCP) more than WireGuard, whose inner TCP still uses GSO on wg0 (see Diagnostics: `gso_max_segs 65535`), so the SSH-to-tunnel throughput ratios on the LAN are on the pessimistic side for plain SSH.

## Environment

| Item | Value |
|------|-------|
| CPU | Intel(R) Core(TM) i9-14900K (32 logical cores) |
| Host kernel | 6.6.123.2-microsoft-standard-WSL2 |
| Docker | 29.7.2 |
| Container image | debian:bookworm-slim (e2e/images/tw) |
| tw | tw version v3.0.0-2-gc8b8195-dirty |
| OpenSSH | OpenSSH_9.2p1 Debian-2+deb12u10, OpenSSL 3.0.20 7 Apr 2026 |
| iperf3 | iperf 3.12 (cJSON 1.7.15) |
| netperf | Netperf version 2.7.0 |
| WireGuard | kernel module 1.0.0 |
| WAN emulation | 30ms:0.1% |
| NIC offloads | tso/gso/gro off + gso_max_segs 1 on client, server and relay eth0 for the whole run (TCP always builds GSO skbs unless gso_max_segs caps it; netem drops per skb, so this makes loss per 1500-byte packet for every transport; LAN ceilings are therefore software-segmentation ceilings) |
| scp form / options | scp · `-c aes128-gcm@openssh.com -o Compression=no -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -i /bench/id_ed25519` |
| Runs per cell | 3 timed (after 1 warm-up); cells show median (min–max) |
| scp payload | 5 GiB of /dev/urandom on LAN, the 256 MiB warm-up file on WAN (TCP loss-bound ≈2 MB/s makes 5 GiB impractical); served from tmpfs, written to /dev/null |

## Results — LAN (0 ms RTT, no loss)

| Transport | iperf3 1 stream (Gbit/s) | iperf3 4 streams (Gbit/s) | TCP_RR (trans/s) | TCP_RR p50 / p99 (µs) | scp 5 GiB (MB/s) | scp wall (s) | wire overhead | Gbit/s per busy core | CPU-s per scp run client / server / relay |
|---|---|---|---|---|---|---|---|---|---|
| Pure SSH | 3.64 (3.63–3.65) | 3.68 (3.65–3.73) | 30511 (23558–31056) | 29 / 75 | 359 (342–361) | 14.9 | 4.7 % | 1.37 | 12.9 / 17.1 / 0.0 |
| SSH over WireGuard | 1.20 (1.14–1.22) | 1.20 (1.18–1.21) | 6567 (6539–6586) | 135 / 336 | 149 (147–149) | 36.1 | 9.4 % | 0.29 | 23.1 / 9.0 / 0.0 |
| SSH over Tunnel Whisperer | 0.97 (0.96–0.99) | 1.09 (1.09–1.14) | 1339 (1333–1340) | 682 / 1162 | 122 (121–125) | 43.9 | 6.0 % | 0.12 | 93.0 / 86.6 / 227.8 |

## Results — WAN (30 ms RTT, 0.1 % loss)

| Transport | iperf3 1 stream (Gbit/s) | iperf3 4 streams (Gbit/s) | TCP_RR (trans/s) | TCP_RR p50 / p99 (µs) | scp 256 MiB (MB/s) | scp wall (s) | wire overhead | Gbit/s per busy core | CPU-s per scp run client / server / relay |
|---|---|---|---|---|---|---|---|---|---|
| Pure SSH | 0.03 (0.03–0.03) | 0.10 (0.07–0.11) | 32 (31–33) | 30503 / 30993 | 2 (2–2) | 143.3 | 4.7 % | 0.04 | 0.9 / 0.9 / 0.0 |
| SSH over WireGuard | 0.03 (0.03–0.03) | 0.10 (0.08–0.10) | 33 (32–33) | 30510 / 32666 | 2 (2–2) | 144.9 | 9.3 % | 0.03 | 1.7 / 0.9 / 0.0 |
| SSH over Tunnel Whisperer | 0.04 (0.03–0.04) | 0.03 (0.03–0.03) | 31 (31–32) | 31524 / 32909 | 4 (3–4) | 76.7 | 6.1 % | 0.04 | 4.4 / 3.7 / 12.9 |

The WAN scp column is the 256 MiB warm-up file, not the 5 GiB LAN file. At the measured WAN scp throughput (2–4 MB/s median across the three transports) a 5 GiB copy would take roughly 22–45 minutes per run on a link this lossy, for any of the three — that is why the WAN scp payload is 256 MiB instead.

## Memory footprint

Peak resident set (VmHWM) of the transport processes. The baseline rows are idle listeners sampled after the SSH transport's runs (the first transport) closed their connections; VmHWM is a since-start high-water mark, so a higher peak reached later by these shared listeners is not captured. The tw and WireGuard rows are transfer-time peaks sampled at the end of that transport's own runs. WireGuard has no userspace process: its footprint is kernel memory, reported as 0.

| Transport | Process | Peak RSS |
|---|---|---|
| Baseline (all transports; idle listeners after the runs) | server / iperf3 | 3.8 MiB |
| Baseline (all transports; idle listeners after the runs) | server / netserver | 1.4 MiB |
| Baseline (all transports; idle listeners after the runs) | server / sshd | 4.6 MiB |
| SSH over WireGuard | kernel (no userspace process) | 0.0 MiB |
| SSH over Tunnel Whisperer | client / tw client connect | 55.6 MiB |
| SSH over Tunnel Whisperer | relay / caddy | 68.4 MiB |
| SSH over Tunnel Whisperer | relay / xray | 41.4 MiB |
| SSH over Tunnel Whisperer | server / tw server start | 45.8 MiB |

The report's `## Diagnostics` section (`e2e/bench-report.md`) records the raw `tc -s qdisc show` output captured on both legs of every WAN cell, plus wg0's link state, so a result can be checked against netem's own counters instead of taken on faith. Across every transport and every cell the drop ratio netem itself reports is 0.090–0.100 %, matching the configured 0.1 % target — evidence that loss was applied per packet, uniformly, regardless of transport. The same capture shows `wg0` running at MTU 1420.

## Reading the numbers

- **Where tw's cost goes.** The CPU-s-per-scp-run columns put most of tw's cost on the relay, not the endpoints: on the LAN scp run the relay burns 227.8 CPU-s against 93.0 (client) and 86.6 (server) — that's Caddy and Xray doing TLS termination and mTLS-gated forwarding for every byte. On the WAN run the same shape holds at a smaller scale (12.9 relay vs. 4.4 client / 3.7 server). scp through tw also pays for an extra inner OpenSSH layer — tw's own end-to-end SSH tunnel running inside the TLS tunnel — on top of whatever the client's own scp session costs.
- **iperf3 and scp agree.** tw's LAN single-stream iperf3 figure (0.97 Gbit/s) and its scp run (122 MB/s ≈ 0.98 Gbit/s) agree within noise.
- **Throughput vs. latency.** TCP_RR p50 through tw is 682 µs against 29 µs for plain SSH on the LAN — the two relay hops and XHTTP framing add about 653 µs; p99 is 1162 µs against 75 µs. On the WAN condition the 30 ms RTT dominates: TCP_RR lands in the same 31–33 trans/s range for all three transports (32 Pure SSH, 33 WireGuard, 31 tw) — indistinguishable at three runs.
- **Under loss, single-stream is a wash.** Single-stream iperf3 on the WAN link is 0.03 Gbit/s (Pure SSH, range 0.03–0.03), 0.03 (WireGuard, 0.03–0.03) and 0.04 (tw, 0.03–0.04) — indistinguishable at three runs. WAN scp shows tw a little higher (4 MB/s, range 3–4) than Pure SSH and WireGuard (2 MB/s, range 2–2 each), but three runs at this spread isn't enough to call tw the fastest under loss as a headline.
- **Under loss, 4 streams is a real difference — and tw doesn't scale.** At 4 parallel iperf3 streams on the WAN link, Pure SSH reaches 0.10 Gbit/s (0.07–0.11) and WireGuard 0.10 (0.08–0.10), while tw stays flat at 0.03 (0.03–0.03) — the same as its own single-stream number. tw multiplexes every stream over one outer TCP connection to the relay, so parallel iperf3 streams share one congestion-controlled pipe instead of getting independent loss recovery. That is a property of the design, not noise, and it is a limitation under loss: tw does not gain from parallelism the way a transport with independent per-stream connections does. It is a separate effect from the relay's mid-path placement described above — the flat 4-stream number is about multiplexing, not about the extra 15 ms leg.
- **WireGuard is one hop shorter — and its numbers here are harness-specific.** It runs peer-to-peer in this benchmark; tw always crosses a relay. That's a property of the products, not a flaw in the measurement — the [comparison table](../index.md#market-comparison) explains why you'd still pick tw (UDP blocked, port-only access, identities on your own server). WireGuard's figures above (1.20 Gbit/s LAN single-stream, 0.29 Gbit/s per busy core) are this harness's numbers: kernel WireGuard running inside a nested WSL2 VM, with all NIC offloads off and an MTU of 1420 (see Diagnostics, 0.094–0.097 % netem drops on its WAN cells) — well below what kernel WireGuard achieves on bare metal. Don't extrapolate these numbers to a bare-metal WireGuard deployment.
- **Historical context.** An earlier manual measurement on v1.5.x over a real ~30 ms Hetzner relay saw ~168 Mbps bulk and ~530 ms pgbench latency at 50 concurrent synchronous clients; the pgbench workload is not part of the automated benchmark.

## Reproduce it

```bash
sudo modprobe wireguard sch_netem   # once per boot; kernel WireGuard + netem
make bench                          # ~35 min from a clean topology; tears down any existing e2e topology first
```

Knobs: `BENCH_SIZE_GB` (5), `BENCH_RUNS` (3), `BENCH_WAN` (`30ms:0.1%`, or `off` for LAN only), `E2E_KEEP=1` to leave the topology up. Only Docker and Go are needed; the relay, server and client are provisioned by the same code paths as `make e2e`. Writes `e2e/bench-report.md` and `e2e/bench-results.json`.

The benchmark is `e2e/bench_test.go`; its design is documented in the repository's spec `.claude/superpowers/specs/2026-08-29-tunnel-benchmark-design.md`.
