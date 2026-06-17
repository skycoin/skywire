# Post-Quantum Hybrid Noise Handshake (design sketch)

Status: **DESIGN — not implemented.** Author-of-record: testing regime. Target: `crypto/mlkem` (Go 1.24 stdlib), our existing Noise_KK transport handshake.

## 1. Motivation — Harvest Now, Decrypt Later (HNDL)

Every skywire transport (stcpr/sudph/stcp/dmsg) is encrypted with **Noise_KK over X25519** (Curve25519 DH) — see `pkg/dmsg/noise/noise.go`, `pkg/transport/network/connection.go:153`. X25519 is broken by a sufficiently large quantum computer (Shor). The realistic near-term threat is **not** a live quantum MITM; it's an adversary **recording ciphertext today** and decrypting it years later once quantum hardware exists. Mesh traffic (control-plane, forwarded apps, chat, VPN) is long-lived-sensitive, so HNDL is the relevant risk and it argues for acting **before** the hardware exists.

## 2. Goal

Add a **hybrid** key exchange: combine the existing **X25519** shared secret with an **ML-KEM-768** shared secret so the session key is secure unless **both** are broken. Hybrid (not ML-KEM-only) because ML-KEM is younger than X25519 — defense-in-depth against a flaw in *either* primitive. This matches the industry consensus (TLS `X25519MLKEM768`, Signal PQXDH, SSH `sntrup761x25519`).

## 3. Current handshake (what we're extending)

- Pattern: **Noise_KK** — both parties know each other's static PK up front (we already have it from TPD / the dial target), mutual authentication.
- Messages: `→ e, es, ss` then `← e, ee, se`; `Split()` yields the `enc`/`dec` `CipherState`s.
- **Key property we exploit:** `hs.WriteMessage(out, payload)` / `ReadMessage(out, msg)` carry an arbitrary **payload**, today passed `nil`. In KK both handshake payloads are **encrypted + authenticated** (the first under `es+ss`, the reply under `ee+se`). So anything we put in the payloads is tamper-evident.

## 4. Construction

Piggyback the KEM in the existing two handshake messages — **no extra round trips**:

```
Initiator → Responder  (msg 1 payload):  mlkem_pub  (ML-KEM-768 ephemeral encapsulation key, 1184 B)  || caps
Responder → Initiator  (msg 2 payload):  mlkem_ct   (ciphertext = Encaps(mlkem_pub), 1088 B)           || caps
```

1. Initiator generates an **ephemeral** ML-KEM-768 keypair, sends the public key in msg-1's payload.
2. Responder `Encaps(mlkem_pub) → (mlkem_ct, ss_pq)`, sends `mlkem_ct` in msg-2's payload.
3. Initiator `Decaps(mlkem_ct) → ss_pq`.
4. **Mix `ss_pq` into the transport keys.** After Noise `Split()` gives the classical `enc/dec` keys `k_c`, derive the actual transport keys:
   ```
   k_final = HKDF-SHA256( salt = noise_handshake_hash,
                          ikm  = k_c || ss_pq,
                          info = "skywire-pq-hybrid-v1" )
   ```
   then re-key the `CipherState`s with `k_final`. Breaking the session now requires breaking X25519 **and** ML-KEM-768.

Ephemeral ML-KEM keypair per handshake ⇒ forward secrecy for the PQ leg too (matches the existing ephemeral X25519 `e`).

### Why mix-into-keys rather than substitute the DH
ML-KEM is a **KEM**, not a DH: the two sides don't run a symmetric `DH(priv,pub)` — one **encapsulates**, one **decapsulates**. So it can't drop into Noise's `DH()` slot. Mixing `ss_pq` into the key schedule *after* the classical handshake (a) keeps Noise_KK's mutual auth untouched, (b) needs no changes to the (ancient, unmaintained) `skycoin/noise` fork's internals, (c) is the construction the PQ-Noise literature endorses for retrofits.

## 5. Downgrade safety (the hard part)

A network attacker must not be able to force two PQ-capable peers down to classical-only (which *would* be HNDL-harvestable).

- The `caps` byte + `mlkem_pub` ride in the **authenticated** KK payload, so a MITM **cannot strip or alter** them without breaking the Noise MAC. ⇒ Two KK peers cannot be silently downgraded by an on-path attacker.
- A *genuine* classical-only peer (old version) simply sends no `mlkem_ct`; the initiator sees an authenticated "no PQ" and proceeds classical. That's an honest capability mismatch, not an attack.
- **Strengthening for the migration tail:** advertise PQ support as a **signed** field in the visor's TPD/discovery entry. Once a peer is *known* (via signed discovery) to be PQ-capable, **require** hybrid with it and **fail closed** on a missing/!valid `mlkem_ct`. This closes the residual "first contact" downgrade window.

## 6. Cost

- **Bytes:** +~2.3 KB per handshake (1184 pub + 1088 ct). Negligible for stcpr/sudph (TCP/KCP). For **dmsg** (relayed) it's two extra relayed KB on connection setup only — acceptable; not per-packet.
- **CPU:** ML-KEM-768 keygen/encaps/decaps are sub-millisecond; trivial vs the network RTT. Zero per-packet cost (only the handshake changes).
- **No new round trips** (payload piggyback).

## 7. Negotiation & fleet migration (1500+ mixed-version visors)

1. **Phase 0 — capability ship:** release understands hybrid, **advertises** PQ support (signed TPD field), but still completes classical with non-PQ peers. No behavior change on the wire yet beyond an optional payload.
2. **Phase 1 — opportunistic hybrid:** when both payloads are present, do hybrid. Most-of-fleet coverage as it updates.
3. **Phase 2 — require between known-PQ peers:** once signed discovery says both support it, fail closed without hybrid (closes the downgrade tail).
4. **Phase 3 — mandatory** once the fleet floor is PQ-capable (gated on the uptime-tracker version census we already have — cf. the version-distribution query).

## 8. Implementation plan (when greenlit)

- `crypto/mlkem` (stdlib, Go 1.24+) — `mlkem.GenerateKey768()`, `EncapsulationKey.Encapsulate()`, `DecapsulationKey.Decapsulate()`. No new dependency.
- New `pkg/dmsg/noise/pqhybrid.go`: keygen, payload (de)serialization, `ss_pq` derivation, the HKDF mix + CipherState re-key. Keep it *beside* `noise.go`, gated by a `caps` bit, so the classical path is byte-identical when PQ is off.
- Wire `MakeHandshakeMessage`/`ProcessHandshakeMessage` to carry the payloads (they already thread a payload arg).
- Tests: ML-KEM **KATs** (FIPS-203 known-answer vectors), hybrid-equals-on-both-ends, **downgrade-rejection** (tampered/stripped payload must fail when PQ required), mixed-version interop (PQ↔classical still connects), and `synctest`-driven handshake-timeout behavior (cf. #23).

## 9. Open questions / risks

- **ML-KEM-768 vs -1024:** 768 (NIST level 3) is the TLS/Signal default and the right balance; revisit if a higher assurance bar is set.
- **The `skycoin/noise` fork is from 2018.** This design deliberately avoids touching its internals (mix at the key-schedule boundary), but the fork's age is a separate liability worth a parallel "move to maintained noise / audit the fork" task.
- **Re-key vs fresh CipherState:** prefer deriving fresh `CipherState`s from `k_final` over re-keying in place, to keep nonce/counter state unambiguous.
- **Signed-capability bootstrap:** depends on TPD entries being signed (they're PK-authenticated already); confirm the discovery entry can carry + sign an extra field.
- Not a substitute for, but composes with, any future static-key PQ signatures (ML-DSA) — out of scope here; this protects *confidentiality* (HNDL), not long-term identity.
