# Tunnel Whisperer throughput & footprint benchmark

Generated: 2026-08-29T22:51:27Z

Three transports, same workload: the client pulls data from the server over plain SSH, over SSH inside a direct kernel-WireGuard link, and over SSH inside the real Tunnel Whisperer path (`tw client connect` → VLESS/XHTTP/mTLS :443 → relay Caddy + Xray → reverse SSH → `tw serve` → sshd). Every instrument moves bytes server → client.

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

## Diagnostics

Raw tc/wg state captured per WAN cell, so a result can be reconciled against the qdisc's own drop/overlimit counters (and, for WireGuard, wg0's MTU and transfer stats) instead of taken on faith.

### ssh / wan — client tc -s qdisc show dev eth0

```
qdisc netem 8014: root refcnt 33 limit 100000 delay 15ms loss 0.1%
 Sent 6209807 bytes 84644 pkt (dropped 83, overlimits 0 requeues 0) 
 backlog 0b 0p requeues 0
drop ratio: 83/(84644+83) = 0.098 %
```

### ssh / wan — server tc -s qdisc show dev eth0

```
qdisc netem 8015: root refcnt 33 limit 100000 delay 15ms loss 0.1%
 Sent 1401600306 bytes 928299 pkt (dropped 929, overlimits 0 requeues 0) 
 backlog 0b 0p requeues 0
drop ratio: 929/(928299+929) = 0.100 %
```

### wireguard / wan — client tc -s qdisc show dev eth0

```
qdisc netem 8016: root refcnt 33 limit 100000 delay 15ms loss 0.1%
 Sent 31692814 bytes 226071 pkt (dropped 212, overlimits 0 requeues 0) 
 backlog 0b 0p requeues 0
drop ratio: 212/(226071+212) = 0.094 %
```

### wireguard / wan — server tc -s qdisc show dev eth0

```
qdisc netem 8017: root refcnt 33 limit 100000 delay 15ms loss 0.1%
 Sent 1464198571 bytes 981430 pkt (dropped 956, overlimits 0 requeues 0) 
 backlog 0b 0p requeues 0
drop ratio: 956/(981430+956) = 0.097 %
```

### wireguard / wan — client ip -d link show wg0

```
3: wg0: <POINTOPOINT,NOARP,UP,LOWER_UP> mtu 1420 qdisc noqueue state UNKNOWN mode DEFAULT group default qlen 1000
    link/none  promiscuity 0  allmulti 0 minmtu 0 maxmtu 2147483552 
    wireguard addrgenmode none numtxqueues 1 numrxqueues 1 gso_max_size 65536 gso_max_segs 65535 tso_max_size 65536 tso_max_segs 65535 gro_max_size 65536
```

### wireguard / wan — client wg show wg0 transfer

```
TlXctjWwZdwxseEP/l0s3sUwZhR2Z1/vx92t2kJRRRM=	29028903448	565651264
```

### tw / wan — client tc -s qdisc show dev eth0

```
qdisc netem 8018: root refcnt 33 limit 100000 delay 15ms loss 0.1%
 Sent 31928099 bytes 346477 pkt (dropped 311, overlimits 0 requeues 0) 
 backlog 0b 0p requeues 0
drop ratio: 311/(346477+311) = 0.090 %
```

### tw / wan — server tc -s qdisc show dev eth0

```
qdisc netem 8019: root refcnt 33 limit 100000 delay 15ms loss 0.1%
 Sent 1260218223 bytes 876801 pkt (dropped 860, overlimits 0 requeues 0) 
 backlog 0b 0p requeues 0
drop ratio: 860/(876801+860) = 0.098 %
```

## Reading the numbers

- **Pure SSH** is the ceiling: one OpenSSH connection straight across the Compose bridge.
- **WireGuard** is a direct peer-to-peer link (mesh-style): one hop fewer than tw, encryption in the kernel — its CPU shows up only in the host-wide CPU-seconds, never in a container's cgroup.
- **Tunnel Whisperer** is the real product path through the relay. For scp that is SSH inside tw's own end-to-end SSH inside TLS; the iperf3 rows have no inner OpenSSH and are tw's own ceiling.
- LAN cells measure encryption/framing cost only (~0 RTT). WAN cells add netem delay and loss on both ends; that is where TCP-over-TLS-over-TCP and UDP diverge.
- Gbit/s per busy core = iperf3 1-stream goodput ÷ average host cores busy during the run.
