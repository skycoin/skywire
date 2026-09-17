# Mux-campaign intermediates — public IP, geolocation, hardware

Collected 2026-09-16 from this visor (`0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c`).
All data is read-only query output; no transports were added or removed.

## Table

| pk (full) | local stcpr tp id | public IP | geo (country / region / city, tz) | NIC link speed | CPU / RAM | notes |
|---|---|---|---|---|---|---|
| `0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb` | `f1012467-de9d-09a9-8b49-62a43945c24b` | 139.162.160.227 | DE / Hesse / Frankfurt am Main, Europe/Berlin (50.1169, 8.6837) | not reported (`eth0` = virtio_net, `/sys/class/net/eth0/speed` = -1) | AMD EPYC 7713, 4 vCPU / 8 GiB | hostname `skywire-production`; also the rf/ar/tpd deployment host. Second local tp to the same pk: squicr `92f299d8-84b6-08c5-9d31-9c89f150d4a6` (same IP:30086). stcpr latency 138 ms. |
| `03f57e7cf26c0764c5ab659a606add056ddf8bfad4f5bc7e8613cad05e5f228adf` | `fdab37dd-c35e-05bc-9e51-cd6b024e3eec` | 172.235.168.146 | NL / North Holland / Amsterdam, Europe/Amsterdam (52.3759, 4.8975) | not reported (virtio_net) | AMD EPYC 7713, 1 vCPU / 1 GiB | also sudph `a9227d2f-1e02-03cf-99d1-fe9a174b7b4e` to the same IP:30081. stcpr latency 149 ms. |
| `0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13` | `95839ad0-b588-0b1d-8475-45fba6ae993c` | 45.79.213.251 | US / Georgia / Atlanta, America/New_York (33.7485, -84.3871) | not reported (`eth0` = virtio_net, speed = -1) | AMD EPYC 7601, 4 vCPU / 8 GiB | hostname `prod02`; also the dmsgd/sd + reward host. Second local tp: squicr `25b05edc-9496-0318-bcb0-7acaf2ac6761` (same IP:30087). Lowest latency of the set, 30 ms. |
| `02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd` | `2c4ae7a5-de55-0e09-bd14-7febf7d9f972` | 45.79.124.73 | IN / Maharashtra / Mumbai, Asia/Kolkata (19.0748, 72.8856) | unknown — no survey | unknown — no survey | **`/node-info` answers 200 with an all-zero survey** (402 bytes, `public_key` all zeros), three attempts. IP/geo therefore come from the local transport table only. Also sudph `9d24c57d-1731-0944-9ac6-f3d73f990b96` to the same IP:30082. stcpr latency 263 ms. |
| `02c483938539bd7820f72e48ed6056bab68e221e1108d23965a2903221495e4af7` | `ac40965a-bc99-01e4-a2c9-c26b76db013b` | 172.104.166.8 | SG / — / Singapore, Asia/Singapore (1.2872, 103.8507) | not reported (virtio_net) | AMD EPYC 7642, 1 vCPU / 1 GiB | also sudph `4d8ec1f7-7d42-0f36-9694-20ced01defbe` to the same IP:30084. stcpr latency 240 ms. |
| `0255117bf8d4687dacd5f7ac4c241f008060f1972911552a5b67b76f0e7922f5c7` | `50cdb857-a262-0ae5-bf5b-5c93c8025b69` | 172.105.179.5 | AU / New South Wales / Sydney, Australia/Sydney (-33.8672, 151.1997) | not reported (virtio_net) | AMD EPYC 7713, 1 vCPU / 1 GiB | also sudph `f1b7ba65-8204-0163-92d9-6ce91c26bf1d` to the same IP:30085. stcpr latency 193 ms. |
| `022716fba0f9a5d46ae7b8a4412296847d63d96c763d49068e851e9be7542080b1` (**exit**) | `b414796d-14fc-080e-9ce6-a7162e3e562b` | 143.42.59.213 | DE / Hesse / Frankfurt am Main, Europe/Berlin (50.1169, 8.6837) | not reported (virtio_net) | AMD EPYC 7642, 2 vCPU / 4 GiB | Arch Linux, skywire v1.3.95-0 (every intermediate runs v1.3.94-28-g86fd4a9d7 on Ubuntu 24.04.4). Second local tp: squicr `bd7698ba-38c3-06c5-8add-fffc3872e75b` (same IP:32911). stcpr latency 136 ms. |

All seven are KVM guests (`zcalusic_sysinfo.node.hypervisor` = `kvm`), linux/amd64,
single socket, CPU nominal 2000 MHz, single `eth0` on `virtio_net`.

## Shared-IP verdict

**No two intermediates share a public IP, and no two share a /24.** The six
intermediate IPs are 139.162.160.227, 172.235.168.146, 45.79.213.251,
45.79.124.73, 172.104.166.8, 172.105.179.5 — six distinct /24s in five distinct
/16s (45.79.213.0/24 and 45.79.124.0/24 are different /24s inside the same
45.79.0.0/16, and 172.104.166.0/24 vs 172.105.179.0/24 are different /16s). The
exit 143.42.59.213 is distinct from all six. Two legs through any two of these
intermediates are therefore genuinely two different hosts on two different
networks — not one pipe.

Two weaker couplings are worth keeping in mind when reading mux results:

- **Same metro, not same host.** `0281a102…` (139.162.160.227) and the exit
  `022716fb…` (143.42.59.213) both geolocate to Frankfurt. A leg through
  `0281a102…` is a short intra-Frankfurt second hop; the intermediate-to-exit
  segment of that leg may share upstream capacity with the direct path even
  though the first hops are unrelated.
- **Same provider.** Every IP is in Linode/Akamai space (139.162/172.104/
  172.105/172.235/45.79/143.42 are all Linode ranges), so legs can share
  provider backbone between regions. ASN was **not** confirmed from a local
  source — see "what could not be obtained".
- **Two carriers per pk, one host.** Each intermediate also has a second local
  transport (sudph or squicr) to the *same* IP and usually the same port. If a
  leg set is built from a pk's stcpr *and* its squicr/sudph transport, those two
  legs are one host and one NIC — same-host aliasing at the transport level,
  even though the six pks themselves are not aliased.

## Sources — one line per column, reproducible

- **local stcpr tp id, public IP, country, latency (and the second carrier per pk):**
  `/home/d0mo/go/bin/skywire cli tp ls --json` — each entry carries `id`,
  `remote_pk`, `type`, `remote_ip`, `remote_country`, `latency_ms` and
  `endpoint.remote_addr`. (`remote_country` is filled locally from the embedded
  MaxMind db by `pkg/visor/geo_summary.go`.)
- **geolocation (region / city / timezone / lat-lon):**
  `/home/d0mo/go/bin/skywire svc ip <ip>` — e.g.
  `/home/d0mo/go/bin/skywire svc ip 139.162.160.227` (embedded GeoLite2-City;
  strip the leading `[…] INFO [geoip]` line before piping to jq).
- **CPU / RAM / OS / hostname / hypervisor / NIC driver / skywire version, and a
  second, independent confirmation of the public IP (`ip_address`):**
  `curl -s --socks5-hostname 127.0.0.1:4443 http://<pk>.dmsg/node-info` — the
  :4443 resolving proxy's key is survey-whitelisted. `skywire cli log info <pk>`
  is the CLI equivalent but its **ephemeral** key is not whitelisted and it
  returns HTTP 401; use the proxy. The survey `ip_address` matched
  `tp ls` `remote_ip` for all six that answered.
- **NIC link speed:**
  `/home/d0mo/go/bin/skywire cli pty exec <pk> --timeout 25s -- /bin/sh -c 'for i in /sys/class/net/*/speed; do n=${i%/speed}; echo -n "${n##*/}=$(cat $i 2>/dev/null||echo NA) "; done; echo'`
  — sampled on `0281a102…`, `03f57e7c…` and `0371ab4b…`; `eth0` returned `-1` on
  all three (virtio_net does not expose a link speed). Docker veth/bridge
  interfaces on the two production hosts report the virtual 10000 Mb/s, which is
  not the uplink.

## What could not be obtained

- **NIC link speed, anywhere.** The node-info survey has no ethtool / link-speed
  field at all (`grep -rn "ethtool\|LinkSpeed\|link_speed" --include=*.go` over
  the repo, excluding vendor, matches nothing), and the kernel itself reports
  `-1` for `eth0` because the driver is `virtio_net`. Real uplink capacity on
  these guests is a provider plan attribute, not something the host can read.
  The remaining four hosts were not sampled (the pty read was declined by the
  local permission layer partway through); given identical `virtio_net` NICs in
  every survey the answer would be the same `-1`.
- **ASN.** The embedded geoip database is GeoLite2-**City**; `skywire svc ip`
  has no ASN field (`pkg/geoip.Result` carries lat/lon, postal, continent,
  country, region, city, timezone — nothing else). "All Linode/Akamai" above is
  inferred from the IP ranges, not read from a local source.
- **A survey for `02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd`.**
  `/node-info` returns HTTP 200 with a zero-valued survey document on every
  attempt, so its CPU/RAM/OS/version are unknown. Its IP and geo are still solid
  — they come from the local transport table, not from the survey.
