# WASM routing policies

This directory holds **WebAssembly** alternatives to the Starlark
(skylark) policies in `../`. Both backends produce the same
`policy.RouteSpec` per dial and plug into the same `router.DialHook`
via the shared `policy.Engine` interface — operators pick whichever
backend fits their tooling.

## When to pick which backend

| feature                | skylark (Starlark)                | WASM                                  |
|------------------------|-----------------------------------|---------------------------------------|
| source format          | text `.star` file                 | compiled `.wasm` artifact             |
| edit-test loop         | save → fsnotify reload → live     | recompile → save → fsnotify reload    |
| guest languages        | Starlark only                     | Go / Rust / AssemblyScript / Zig / C  |
| stdlib                 | `geo.*`, `transports.*`, `peers.*`| same exports, host-imported           |
| per-call cost          | bytecode interp, few µs           | wasm call, ~µs                        |
| ergonomics             | great for <50-line policies       | great when you already have Go/Rust   |
| review surface         | one text file                     | bytes blob + source-of-record         |
| works out of the box   | yes                               | needs a guest toolchain (e.g. TinyGo) |

Default to skylark. Reach for WASM when:
- you want to share a Go struct between visor and policy without
  re-encoding it in Starlark
- you want to ship a single compiled policy across many visors and
  not depend on an interpreter staying source-compatible
- the policy uses non-trivial libraries you don't want to re-implement
  in Starlark

## ABI

A WASM module ships as a single `.wasm` artifact and must export:

```
alloc(size: i32) -> ptr: i32       // host-driven malloc; freed by guest GC or manual free()
free(ptr: i32, size: i32)          // no-op safe under host-driven GC
decide_route(in_ptr: i32, in_len: i32) -> packed: i64
on_leg_change(in_ptr: i32, in_len: i32) -> packed: i64   // optional
```

The packed return is `(offset | length << 32)`. The host writes the
JSON-encoded input into a guest buffer returned by `alloc`, calls
`decide_route`, reads the same packed `(offset|len)` from the result,
and JSON-decodes a `RouteSpec`. The wire types live in
[`pkg/router/policy/wasm/abi.go`](../../../../pkg/router/policy/wasm/abi.go).

The host stdlib (imported by the guest from the `"skywire"` module):

```
skywire.geo_country(pk_ptr, pk_len) -> packed_ptr_len
skywire.transports_latency(pk_ptr, pk_len) -> ms
skywire.transports_kind(pk_ptr, pk_len) -> packed_ptr_len
skywire.peers_is_trusted(pk_ptr, pk_len) -> 0|1
skywire.peers_is_hypervisor(pk_ptr, pk_len) -> 0|1
skywire.log_info(msg_ptr, msg_len)
skywire.log_warn(msg_ptr, msg_len)
```

These mirror the Starlark `geo.*`, `transports.*`, `peers.*` stdlib
1:1 — porting a `.star` policy to Go-WASM is mostly the JSON wiring.

## Building an example (TinyGo / Go)

```bash
cd docs/examples/routing-policies/wasm/app-mux
tinygo build -target=wasi -no-debug -opt=2 -o app-mux.wasm .
```

Required: TinyGo 0.32+ (uses the `wasi` target). Standard `go build
-target=wasi` does not produce a WASI module — Go's normal WASM output
is `js/wasm`, which can't be loaded by wazero.

Install:

```bash
sudo install -m 0644 app-mux.wasm /etc/skywire/policies/
```

Point the visor at it via `skywire.json` — same field as the
skylark policy; backend is picked by file extension:

```json
"routing": {
  "policy_per_dial": "@/etc/skywire/policies/app-mux.wasm"
}
```

Per-app overrides accept WASM too:

```json
"launcher": {
  "apps": [
    {
      "name": "vpn-client",
      "routing_policy": "@/etc/skywire/policies/vpn.wasm"
    }
  ]
}
```

Or supply at app-start time via CLI:

```bash
skywire cli visor app start vpn-client \
    --routing-policy @/etc/skywire/policies/vpn.wasm
```

## Hot reload

Same fsnotify watcher as the skylark loader, 200 ms debounce. Reload
failures keep the previous module active so a broken rebuild can't
take the visor offline. Successful reloads log
`wasm policy <path>: reloaded`.
