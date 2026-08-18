# Per-leg mux telemetry harness

The routing-policy rig needs to *prove* a WASM policy is adaptive and better —
per leg, over time, against a controlled far-end. This harness is the
measurement half.

## What it samples

`skywire cli proxy mux info --ndjson FILE` polls the live route group for an app
(default `skysocks-client`) once per `--watch` interval (default `1s`, i.e.
≥1 Hz) and writes newline-delimited JSON. It reads the **same**
`rg.snapshotLegs()` the policy `on_tick` ABI sees, so the harness and the policy
observe one source of truth.

Two record shapes, all on one clock (the sample tick's timestamp):

**`sample`** — one per route group per tick:

```json
{"ts":"2026-08-18T15:40:01.002Z","type":"sample","app":"skysocks-client",
 "rg":"<src>:3>...:44","legs":[
   {"route_index":0,"intermediate_pk":"02ab..","transport_kind":"dmsg",
    "inst_send_bps":81234,"inst_recv_bps":512901,"rtt_ms":42,
    "retransmits":3,"gate_state":"active","alive":true}]}
```

**lifecycle events** — derived by diffing each tick's leg set against the last:

| type          | fires when                                    |
|---------------|-----------------------------------------------|
| `established` | a leg index appears (or first poll)           |
| `promoted`    | a leg's `gate_state` goes `standby` → `active` |
| `demoted`     | a leg's `gate_state` goes `active` → `standby` |
| `failed`      | a present leg goes `alive:false`              |
| `dropped`     | a leg index disappears                         |

```json
{"ts":"2026-08-18T15:40:11.010Z","type":"promoted","app":"skysocks-client",
 "rg":"<src>:3>...:44","route_index":3,"intermediate_pk":"02cd..","transport_kind":"dmsg"}
```

Because events are polled, their resolution is the sample interval — a leg that
appears and vanishes inside one tick is not seen. `gate_state` transitions and
drops persist, so a ≥1 Hz poll catches them on the next tick.

## Usage

```sh
# Stream to a file for a fixed 5-minute rig run:
skywire cli proxy mux info --ndjson run.ndjson --watch 1s --duration 5m

# Or to stdout, piped:
skywire cli proxy mux info --ndjson - --watch 500ms | tee run.ndjson

# Query a different app:
skywire cli proxy mux info -n vpn-client --ndjson run.ndjson
```

## Charting

Open `mux-telemetry-chart.html` in a browser and load `run.ndjson` (file picker
or drag-and-drop). It renders, per leg:

- **bandwidth** — inst send/recv bps over time, and
- **RTT** — per-leg `rtt_ms`,

with policy lifecycle events drawn as vertical markers. A warm-standby hot-swap
(gate-5) shows as a `demoted`+`promoted` marker pair with the aggregate
throughput line staying flat across it — the "no capacity dip" proof.

The page is fully self-contained (no external scripts/fonts/network) — it works
opened directly from disk.
