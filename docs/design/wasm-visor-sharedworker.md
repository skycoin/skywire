# One wasm-visor per origin — SharedWorker-hosted visor

## Goal

A single wasm-visor that runs **as long as any tab of the origin is open**, not
one-per-tab. Concretely:

- Open a tab → the visor boots (once).
- Open more tabs of the same origin → they **share** the running visor. No second
  boot, no dmsg re-register, no "already running in another tab" notice.
- Close a tab → the visor **keeps running** as long as any other tab is open.
- Close the **last** tab → the visor stops.

This replaces the current model (one visor per tab + a Web Locks single-instance
guard that reload-hands-off the identity between tabs, paying a full ~10–20s
re-boot each time). The guard prevents two clients colliding, but it does **not**
preserve the running visor — which is the actual requirement.

## Mechanism: SharedWorker

A `SharedWorker` is exactly this lifecycle primitive: one instance shared by all
same-origin documents, alive while **any** connected document is open, terminated
when the last disconnects. Tabs attach via `MessagePort`.

```
  ┌── tab A ──┐   ┌── tab B ──┐   ┌── tab C ──┐     (HV UI / browse.js — thin views)
  │  hvApi →  │   │  hvApi →  │   │  hvApi →  │
  └────┬──────┘   └────┬──────┘   └────┬──────┘
       │ MessagePort   │               │
       └───────────────┴───────────────┘
                       │
             ┌─────────▼──────────┐
             │   SharedWorker     │   ONE wasm-visor: dmsg client + router +
             │   wasm-visor core  │   autoconnect + in-wasm hvApi. Lives while
             └────────────────────┘   any tab is connected.
```

No leader election, no takeover, no re-boot on switch/close. Switching tabs is
instant (the UI just talks to the already-running worker).

## Feasibility (checked)

- **Go visor core is worker-safe.** `cmd/wasm-visor/*.go` touches no `document`,
  `window`, or `localStorage` — those are all in `hv-boot.js`/`override.js`. The
  core is network + compute (`syscall/js` for WS/WT/fetch), which works in a
  worker.
- **WebSocket**: available in workers. dmsg-over-WS works.
- **WebTransport in a SharedWorker**: Chromium exposes WebTransport in workers
  (verify on target Brave/Chrome versions). If a given engine lacks it, WS-only
  still works.
- **WebRTC — kept, via a tab-hosted agent (NOT dropped).** `RTCPeerConnection`
  is unavailable in any worker, but WebRTC is the one carrier that reaches a
  peer through NAT without port-forwarding, so it must survive this move. The
  solution: the **worker orchestrates, a tab executes**. See the dedicated
  section below.
- **Identity/key**: the worker has no `localStorage`. A connecting tab passes the
  key (from its `localStorage`, as today) to the worker on first connect; the
  worker boots once with it. (Or move the key to IndexedDB, which workers can
  read — but tab-passes-key is simpler and keeps the key handling in one place.)

## Bridge protocol (tab ↔ worker over MessagePort)

Two message kinds:

1. **Request/response** — the tab's `hvApi(method, path, body)` becomes
   `port.postMessage({t:'req', id, method, path, body})`; the worker runs the
   in-wasm hvApi and replies `{t:'res', id, status, body, headers}`. A per-id
   promise map on the tab side resolves it. Same shape `SkywireHttpBackend`
   already uses — just a different transport.
2. **Event stream** — callback-style APIs (skychat messages, telemetry, proxy
   verbose logs, the runtime-log ring) are pushed `{t:'ev', topic, data}`; tabs
   subscribe by topic. Replaces the current in-page `js.FuncOf` callbacks.

`globalThis.skywireVisor.*` in a tab becomes a thin shim that forwards to the
port (so `browse.js` and the Angular `SkywireHttpBackend` need only a new
"worker" routing mode; their call sites are unchanged).

## WebRTC in worker mode — tab-hosted agent

WebRTC is essential (the only NAT-piercing carrier), so it can't be dropped when
the core moves into the worker. The worker can't call `RTCPeerConnection`, but a
**tab** can — so we split the WebRTC carrier into an *orchestrator* (worker) and
an *executor* (tab):

- The worker keeps everything that doesn't need `RTCPeerConnection`: the transport
  manager, the **signaling** (WebRTC offer/answer/ICE are exchanged over dmsg,
  which the worker has), and the decision of when to open/accept a WebRTC
  transport.
- One connected tab is the **WebRTC agent**. The worker drives it over the
  MessagePort with a small command set — `createPC`, `setRemote`, `addICE`,
  `createDataChannel`, `send(bytes)`, `close` — and the tab streams results back:
  local SDP, local ICE candidates, `datachannel.onopen`, and **inbound data**.
- **Data path**: once the `RTCDataChannel` is open in the tab, transport bytes
  relay tab ↔ worker over the port as transferable `ArrayBuffer`s (zero-copy, so
  the extra hop is cheap; tab↔SharedWorker is same-process). The worker's
  transport/dmsg logic sees a normal byte stream; it just physically flows
  through the agent tab.

So the topology for a WebRTC transport is:

```
  remote peer  ⇄  RTCDataChannel (in the AGENT TAB)  ⇄  MessagePort  ⇄  worker (transport logic)
                        ▲ signaling (SDP/ICE) exchanged over dmsg by the worker ▲
```

### Exactly one agent — tabs never make their own WebRTC transports

The worry "what if two tabs both make WebRTC transports?" can't happen here,
because **tabs don't make transports at all — the worker does.** There is one
visor (in the worker) with one transport manager; it delegates execution to
**exactly one** agent tab at a time (the worker holds a single `agentPort`; only
that port receives WebRTC commands). So:

- There is never more than one `RTCPeerConnection` host. No two tabs race to open
  the same transport; a second/third tab is a pure UI view and hosts nothing.
- **Display is unified, not per-tab.** All tabs render the *same* worker visor's
  state, so the WebRTC transports show identically everywhere — they belong to
  the one visor, not to whichever tab happens to be the agent. The agent role is
  an invisible internal detail; the user never sees or picks it. (This is simpler
  than today's per-tab model, where each tab is a separate visor with its own
  transports.)
- If we ever *did* want per-tab WebRTC (we don't), it would need one visor
  identity per tab — which is exactly the collision the shared-visor model
  removes. So "one agent tab makes the WebRTC transports" isn't a limitation to
  work around; it's the correct shape.

**Prefer a visible tab as the agent.** Browsers throttle background tabs, which
could slow the agent's relay. So the worker prefers a **foreground/visible** tab
as agent (tabs report `document.visibilityState` over the port) and re-elects to
a visible one when the current agent is hidden. If every tab is backgrounded, the
agent keeps running (data channels are throttled far less than timers), just
possibly slower — acceptable, and it recovers the moment a tab is focused.

### Agent-tab churn

If the agent tab closes, its
`RTCPeerConnection`s die. The worker detects the agent port dropping, **re-elects
another open tab** as agent, and the WebRTC transports re-establish through it
(autoconnect re-dials). Crucially, only the *WebRTC transports* blip — the dmsg
session, routes, and the whole visor state live in the worker and are untouched.
So closing any tab (even the agent) never re-boots the visor; at worst a couple
of P2P transports reconnect, which the mesh already self-heals. If NO tab is open,
the worker is gone anyway (that's the intended "last tab closed → stop").

Implementation: `autoconnect_js.go` / the WebRTC client currently call
`RTCPeerConnection` directly via `syscall/js`. In worker mode they instead call a
JS shim (`webrtcAgent.*`) that, in the worker, forwards each op to the agent tab
over the port and awaits the reply. The tab side is a compact agent script that
owns the real `RTCPeerConnection`s. (Same split works for inbound WebRTC —
`peerserve_js.go` — the agent tab hosts the answering PC.)

## Lifecycle

- `worker.onconnect` → push the port into the client set; if this is the first
  client, boot the visor (using the key the tab supplied).
- Tab close → its port `messageerror`/`close`; drop it from the set. The worker
  keeps running while the set is non-empty.
- Last port gone → the SharedWorker is terminated by the browser → visor stops.
  (Optionally: a short grace timer so a full-page reload — which briefly drops to
  zero tabs — doesn't tear the visor down mid-refresh.)
- The Web Locks singleton guard + the "another tab" notice + the takeover button
  are **removed** — they're obsolete once there's one shared visor.

## Phased plan

1. **Spike** ✅ — SharedWorker loads `wasm_exec.js` + the visor `.wasm`, boots the
   core, a tab gets `/api` over the port; dmsg over WS. The wasm boots cleanly in a
   SharedWorker on Chrome 149.
2. **Multi-tab fan-out** ✅ — per-tab ports; open/close tabs without re-boot; the
   visor survives closing the first tab.
3. **UI routing** ✅ — the `skywireVisor` proxy over the port is unchanged in shape,
   so `browse.js` and the Angular `SkywireHttpBackend` route through it unedited.
4. **WebRTC tab-agent** ✅ (bridge live-validated) — the worker orchestrates
   (transport manager + dmsg signaling), a single elected agent tab executes the
   `RTCPeerConnection`; agent re-election on tab close with proper transport
   teardown so autoconnect re-dials. Full two-visor DataChannel transport not yet
   exercised end-to-end (infra: needs a manual dial to a webrtc-listening peer).
5. **Singleton guard** ✅ (bypassed, retained as fallback) — the Web Locks guard is
   skipped on the SharedWorker path and the reload "take over" button dropped; the
   guard remains only as the dedicated-Worker fallback's collision protection, so
   it is not deleted outright.

## Status

**Implemented (phases 1–3) and validated live.** `pkg/wasmhv/worker.js` is now a
SharedWorker that boots the visor once and fans `/api` out to every connected tab's
MessagePort; `pkg/wasmhv/hv-boot.js` prefers it (`bootInSharedWorker`) and only
falls back to the per-tab dedicated Worker + Web Locks guard where SharedWorker is
unavailable. Both hosts speak one worker-owned-boot protocol (`{t:'init'}` →
`{t:'up', pk}`), so the dedicated fallback no longer double-boots either. The
`globalThis.skywireVisor` proxy is unchanged in shape, so `browse.js` and the
Angular `SkywireHttpBackend` route through it without edits (phase 3 came for free).

Validated on Chrome 149 against the local `hv serve` (`:8444`):

- First tab boots the visor once (`status().pk` returns the booted key).
- A second tab attaches to the **same** visor in ~2s — same PK, no re-boot, no
  "already running in another tab" notice.
- Closing the first tab keeps the visor alive for the second (same PK) — the
  running dmsg client/router/identity survive tab churn.

**WebRTC (phase 4) — implemented and bridge validated live.** The worker
delegates STUN + all `RTCPeerConnection` ops to exactly one elected agent tab, so
WebRTC (the only NAT-piercing carrier) is *preserved*, not dropped. Two
correctness fixes landed beyond the initial wiring:

- **Agent-handoff teardown.** When the agent tab closes, its `RTCPeerConnection`s
  die but the Go carrier still holds the proxy objects. `resetRTCForNewAgent` now
  fires `onclose` on every proxy `DataChannel` (→ `webrtc_browser.go` `dcConn.onClose`)
  before dropping them, so the carrier tears the transport down and autoconnect
  re-dials through the new agent (the peer re-offers over dmsg, yielding a fresh
  `pcId` the new agent hosts cleanly). Without this the carrier would think dead
  transports were still alive.
- **No visibility thrash.** The agent role is *not* handed off merely because
  visibility changed — a handoff tears down all live WebRTC transports (they can't
  migrate between tabs), so doing it on every tab switch would thrash. The agent
  keeps its role until it actually closes; visibility is only a preference when a
  fresh agent must be elected. A backgrounded agent tab is throttled far less for
  data channels than for timers (acceptable).

Bridge validated live (Chrome 149, cold boot, `RTCPeerConnection` wrapped on the
agent tab): exactly **one** `RTCPeerConnection` is constructed on the agent tab
with the deployment STUN ICE config, and the visor *in the worker* logs
`self public IP (via STUN): <ip>` — proving the full worker→agentPort→tab→real
`RTCPeerConnection`→worker round-trip. The WebRTC offerer/answerer use the
identical `newPeerConnection` → `__skywireRTC.newPC` → agent delegation. A full
two-visor WebRTC DataChannel transport is not yet exercised end-to-end: WebRTC is
not autoconnected (browser visors autoconnect WS/WT only), so it needs a manual
`dialTransport(pk, "webrtc")` to a peer running the webrtc signaling listener —
infra to arrange, not a code gap.

**Phase 5 — not necessary as originally scoped.** The Web Locks singleton guard is
already **bypassed on the SharedWorker path** (multiple tabs share one visor with
no notice), and the disliked reload-based "take over" button was dropped. The
guard is *retained only* as the dedicated-Worker fallback's protection (where
SharedWorker is unavailable, it still prevents two tabs colliding on one identity).
Fully deleting it would remove that fallback protection for no benefit, so it
stays fallback-only rather than being removed outright.

## Fallback status

Where `SharedWorker` is unavailable, `hv-boot.js` falls back to the dedicated
Worker (UI still off-thread) guarded by Web Locks with the simple auto-handoff
notice, and finally to the in-page runtime if Workers are unavailable at all.
