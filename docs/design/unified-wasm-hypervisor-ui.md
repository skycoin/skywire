# Unified WASM hypervisor UI

Status: proposal
Related: `pkg/wasmhv`, `cmd/dmsg-wasm`, dmsg-over-WebSocket (#3189), WASM
hypervisor core (#3190), dmsg-over-WebTransport (#3193), apps/transports
control endpoints (#3194).

## Problem

We now ship two hypervisor front-ends that do the same job:

- the **visor-served Angular UI** (`http://localhost:8000`), talking to the
  local visor's hypervisor HTTP API; and
- the **WASM hypervisor** (`pkg/wasmhv` + `cmd/dmsg-wasm`), which carries the
  hypervisor logic *in the browser* and reaches visors over dmsg.

Maintaining both is wasteful, and the second one *contains* the hypervisor — so
it can be the canonical front-end. The proposal: **one HTML+wasm artifact** that
detects its own context and picks one of three roles, instead of three separate
builds.

## The three roles, one artifact

The page ships the Angular UI plus the WASM dmsg client + `override.js`. A single
discriminator decides what happens at load:

| Role | Trigger | Behaviour |
|------|---------|-----------|
| **served** (default) | served over `http(s)://` by a real backend, no standalone config injected | `override.js` stays **dormant** — native `XHR`/`fetch` reach the serving visor's `/api`, exactly as today. The wasm is never even booted. |
| **viewer** | `__SKYWIRE_HV__.pk` set (a generated single-file bundle) | route `/api` over dmsg to the **remote** hypervisor PK. |
| **standalone** | `__SKYWIRE_HV__.standalone` set | THIS tab **is** the hypervisor: `serveHypervisor()` (visors dial in on dmsg:46), `/api` → in-wasm core (`hvApi`). |

The key property: **served mode never asks for a key.** It rides the visor's
existing hypervisor session (cookie / CSRF), so the same page the visor serves is
indistinguishable from today's UI. Key entry only happens in the two
deliberately-generated serverless variants.

### Context detection

Not "am I local" but "do I have a mode to take over for." Concretely, in
`override.js`:

```
activate = Boolean(CFG.pk) || Boolean(CFG.standalone)
```

When false (a visor-served page injects no `__SKYWIRE_HV__`, or an explicit
`{served:true}`), the shims are not installed and the wasm is not booted. This is
the foundational primitive (Phase 1 below) and is independently safe: it lets the
same bundle be served by a visor *or* opened from `file://`.

## Security boundary (precise)

The standing rule is "never host the hypervisor UI on a domain," because typing a
secret key into a page served by a remote host is spoofable (the abandoned
skycoin-web wallet failure mode).

This design **tightens** that rule rather than bending it:

> Never serve the **standalone / viewer (key-entry)** variant from a domain. The
> **served** variant is safe to serve from anywhere — including a visor on a
> public interface behind PK-auth — because it takes **no key**: it only ever
> uses the serving backend's own session.

So:

- Replacing the visor-served Angular UI with this artifact (served mode) is safe:
  same trust boundary as today (you already trust that local binary with your
  config and keys).
- The single-file serverless variants (viewer / standalone), which *do* prompt
  for a key, are delivered out-of-band (support chat, file) and **must never** be
  hosted on a domain or CDN. The generator must not emit a domain-hostable form
  of the key-entry variant.

## Two enable-flows (server → serverless)

Going serverless requires generating a **new key** (never reuse the visor
identity — this key becomes fleet authority). The user opts in either from the
CLI or from the served UI, then chooses one of two modes:

### Mode 1 — wasm UI dials a hypervisor (original)

1. Generate the standalone key (password-encrypted at rest).
2. Flush the **public** key to the visor's config as the hypervisor it serves
   for, and bake the key into the generated single-file HTML (`__SKYWIRE_HV__`).
3. Open the file; it dials that hypervisor PK over dmsg.

### Mode 2 — visors dial the wasm UI (inverted)

1. Generate the standalone key.
2. Set that PK as a **remote** `hypervisors:[]` entry on all connected visors, or
   a GUI-selectable subset — via the `AddHypervisor` RPC the hypervisor already
   holds a client for (just unexposed in the UI today). `RemoveHypervisor` /
   `RemoveAllHypervisors` provide the release path.
3. Open the standalone tab; visors dial in on dmsg:46 (reachable even when the
   browser has no inbound IP, via dmsg server bridging — WebTransport makes it
   reachable from a locked-down browser).

The `hypervisors:[]` list is per-visor and additive, so the two modes can coexist
across a fleet, and a persistent server hypervisor can run alongside an ephemeral
wasm co-hypervisor.

## Sharp edges to handle

- **Payload.** Tens of MB of wasm must not load on every served-mode page view.
  Lazy-load the wasm only when the user enters a serverless mode; served mode
  pays nothing. (Reinforces context detection gating the *boot*, not just the
  shims.)
- **Mode-2 revocation.** A visor pointed at a closed tab keeps dialing a dead PK.
  Either accept the harmless self-healing failure loop or surface an explicit
  "release these visors" (`RemoveHypervisor`) action. Never leave a fleet
  silently pointed at a vanished tab without a way to see/undo it.
- **Standalone key lifecycle.** It is now a real credential (fleet authority, in
  Mode 2). Back it up, password-encrypt it (already done via the
  AES-GCM/PBKDF2 `encsk` path), and treat losing it as losing hypervisor control
  of those visors.
- **TinyGo.** Orthogonal. The served/viewer/standalone split is a standard-Go
  js/wasm concern; a TinyGo size win is gated separately on replacing the
  `net/rpc`+gob codec in the core with a reflection-free one.

## Phasing

1. **Context-detection gate** in `override.js` (`activate` discriminator + boot
   gating). Safe, no consumer change. *(This proposal lands Phase 1.)*
2. **Single-file generator** — CLI command producing the viewer/standalone HTML
   (inline wasm base64 + `wasm_exec.js` + Angular UI + `override.js` + injected
   `__SKYWIRE_HV__`, key password-encrypted). The remaining "drop into support
   chat" deliverable.
3. **Serve the artifact from the visor** in place of the Angular build (served
   mode), once 1–2 are proven.
4. **Expose the RPC affordances** in the GUI: "set remote hypervisor" (Mode 1
   write-back) and "make these visors dial me" (`AddHypervisor` over the selected
   subset, Mode 2), plus the release path.

Phases 2–4 each warrant their own review — the key lifecycle and the fleet-wide
`AddHypervisor` exposure are the parts to get right before shipping.
```
