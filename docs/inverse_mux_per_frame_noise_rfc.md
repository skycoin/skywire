# RFC: Single-stream mux aggregation via per-frame noise (inverse-multiplexing)

Status: draft. Companion to `mux_aggregation_rfc.md` (#4212), which took the
*connection*-striped path (many independent flows). This RFC covers the piece
that one deliberately skipped: making a **single** stream (one `curl`) saturate
by striping its bytes across legs and summing them — true inverse-multiplexing —
plus the zero-gap failover that goes with it.

## The one thing that's actually missing

The mux data plane is already sophisticated. Of the three pieces usually named
for "MPTCP-lite", **two already exist**:

- **Throughput-weighted scheduler** — `route_mux.go` `WeightModeCapacity` +
  `rebuildWeights()` feed each leg's recent bytes-delta to the selector
  (`SetCapacityWeights`), so the schedule is weighted ∝ goodput. Selectable via
  the `DistributionCapacity` policy mode (`route_group.go:1054`). (The `proxy
  mux mode` CLI only surfaces `auto`/`equal` today — capacity is policy-only.)
- **Per-leg ack + fast-retransmit-on-death** — `CapSACK`, a sequence-keyed retx
  buffer, `heldRetxSeqs()`/`resendSeqs()`, `selectFastestTransport()` for the
  retransmit path, resend-unacked-on-leg-death, and fast-pruning of
  data-black-holing legs (`route_group.go`).

The **missing** piece — and the sole reason 37 legs still deliver one leg's
worth of goodput — is per-frame crypto:

**Noise is a stateful AEAD applied to the whole ordered byte stream, OUTSIDE the
mux.** `router_serve.go:440` wraps the RouteGroup: `network.EncryptConn(nsConf,
rg)`. So the mux must hand up a perfectly-ordered plaintext-equivalent stream;
`reorder.go` is therefore **no-skip** (its own comment: "delivering PAST a
missing sequence permanently desyncs the cipher — every later frame then
fails"). Any gap on any leg — steady-rate mismatch the weighting can't fully
erase, or ordinary jitter — head-of-line-blocks the entire stream. No scheduler
and no retransmit strategy can beat that ceiling; they only shrink the gaps.

## Fix: move noise from the stream to the frame

Encrypt **each mux DATA frame independently** with an AEAD keyed from the
existing noise handshake, using the frame's **stream sequence number as the
nonce**. Then:

- Frames decrypt **independently and out of order** — no stateful cipher to
  desync, so a late frame on a slow leg never blocks decryption of frames that
  already arrived on fast legs.
- The reorder buffer reassembles **plaintext** by sequence and MAY skip a gap
  (the SACK/retransmit machinery still fills it), because skipping no longer
  corrupts a cipher — it just reorders bytes.
- `network.EncryptConn` is **bypassed** for a per-frame-noise route group; the
  app gets the reassembled plaintext directly.

Sequence-as-nonce is the standard DTLS/QUIC construction and is what makes this
sound: the stream sequence is monotonic and unique per key, so **no nonce is
ever reused** (the one cardinal AEAD rule). Out-of-order arrival is fine because
the nonce travels with the frame (implicit from its sequence), not from a
stateful counter.

## Wire format & negotiation (fleet-safe, incremental)

- New capability bit beside `CapMux`/`CapSACK` in `pkg/routing/packet.go:138`:
  `CapPerFrameNoise uint16 = 1 << 2`. Advertised in the mux handshake
  (`sendHandshake`, `route_group.go:2018`). Per-frame noise is used **only when
  both edges advertise it**; otherwise the group falls back to today's
  stream-noise path. Zero risk to un-upgraded peers — this is why it can ship
  incrementally across the fleet.
- DATA frame gains a fixed AEAD tag (16 B for ChaCha20-Poly1305) and reuses the
  existing per-frame sequence field as the nonce input. No per-frame nonce bytes
  on the wire (derived from sequence + a per-direction salt from the handshake).
- Key derivation: from the completed noise handshake's shared secret, HKDF two
  directional keys (initiator→responder, responder→initiator) + two salts.
  Distinct keys per direction keep the sequence-nonce spaces independent.

## Security analysis (the part that must be right)

- **Nonce uniqueness:** nonce = f(salt_dir, seq). `seq` is a 64-bit monotonic
  per-direction counter that never wraps in any realistic session; rekey (below)
  bounds it regardless. No reuse ⇒ AEAD confidentiality+integrity hold.
- **Replay/reorder:** integrity is per-frame (AEAD tag), so a forged/altered
  frame is rejected. Reorder is expected and benign (reassembly is by sequence).
  A replayed valid frame is dropped by the reorder buffer's `seq < nextSeq`
  discard and the dedup on retransmit — the same guard that exists today.
- **Rekey:** rekey (new HKDF epoch) before `seq` approaches 2^N, carried as a
  handshake-style control frame; the epoch id rides the frame so the receiver
  selects the right key. (Phase 3 — not needed for the first measurable slice
  given 64-bit seq.)
- **Downgrade:** the capability is inside the authenticated handshake, so an
  attacker can't strip `CapPerFrameNoise` to force the weaker/blocking path
  without breaking the handshake.

## What changes, concretely

1. `pkg/routing/packet.go` — add `CapPerFrameNoise`; DATA frame carries an AEAD
   tag when the group negotiated it.
2. `pkg/router/route_group.go` — handshake advertises/negotiates the cap;
   derive per-direction keys+salts on handshake completion; on send, AEAD-seal
   each DATA frame under seq-nonce; on recv, AEAD-open before handing to the
   reorder buffer.
3. `pkg/router/reorder.go` — when per-frame noise is active, the buffer may
   deliver past a gap (skip-capable) after `reorderTimeout`/SACK has had its
   shot; it reassembles plaintext, no cipher to protect. Keep the no-skip path
   for stream-noise groups.
4. `pkg/router/router_serve.go:440` — skip `network.EncryptConn` for per-frame
   groups (the mux already delivers decrypted, ordered bytes).
5. Scheduler: no change needed — `WeightModeCapacity` now actually *aggregates*
   because the HoL wall is gone. Default the proxy/adaptive preset to capacity
   distribution so weighting is on by default.

## Why this delivers the acceptance criteria

- **Single curl saturates:** one stream's frames now decrypt out-of-order and
  reassemble without HoL, so weighted striping sums leg goodput toward the card
  — not capped at one leg. This is the piece the connection-striped RFC could
  not give a single connection.
- **Zero-gap failover:** unchanged mechanism (warm-standby leg + resend-unacked
  over survivors), but now the reorder buffer can skip the dead leg's gap
  immediately instead of stalling the stream until retransmit lands.
- **Converge / shed drags / per-direction scale:** the adaptive tick +
  capacity weighting already do this; they were just masked by the HoL ceiling.

## Relationship to connection-striping (the hybrid)

Inner layer (this RFC): one stream saturates across its legs. Outer layer
(#4212, `--tunnels`): many streams fan across disjoint tunnel sets for
many-connection scale and coarse path diversity. They compose: a single curl
rides the inner inverse-mux to the card; a browser's connections additionally
spread across tunnels. Per-tunnel mux stays small (warm-standby failover), never
a throughput knob.

## Test plan

- Unit: nonce-uniqueness across 10^6 frames; AEAD-open of frames delivered in a
  shuffled order reassembles the original bytes; a dropped-then-retransmitted
  frame reassembles; tamper flips a byte ⇒ AEAD-open fails and the frame is
  dropped (not delivered).
- reorder_test: skip-capable path delivers past a gap only under per-frame
  noise; stream-noise path still refuses to skip.
- parity_test / wasm: the WASM mux path matches native (the wazero/tinygo guard).
- Throughput (live, gated): capacity mode + per-frame noise on a multi-leg group
  → single-stream goodput scales with leg count until the card/exit ceiling,
  vs. the flat one-leg number today. This is acceptance #2's measurement.

## Phasing (each phase independently testable, capability-gated OFF by default)

1. Cap bit + handshake negotiation + key/salt derivation (no data-path change
   yet; assert both edges agree). 
2. Per-frame AEAD seal/open on the send/recv path, behind the negotiated cap;
   `network.EncryptConn` bypass. Unit + parity tests.
3. Skip-capable reorder under per-frame noise; wire to SACK. reorder tests.
4. Default proxy/adaptive to capacity distribution; live throughput measurement
   (acceptance #2). 
5. Rekey epoch (only if a session can plausibly exhaust the seq space).

Phases 1–3 are the crypto core and must be reviewed as a security change before
any fleet enablement; phase 4 is where the summation number finally gets taken.
