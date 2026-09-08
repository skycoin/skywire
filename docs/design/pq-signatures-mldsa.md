# Post-Quantum Signatures (ML-DSA) — capability negotiation and rollout

Companion to [pq-hybrid-noise-handshake.md](pq-hybrid-noise-handshake.md), which
covers key *agreement*. This one covers *signatures*, and reaches a different
conclusion about urgency and scope.

`crypto/mldsa` (FIPS 204) landed in the Go 1.27 standard library, alongside
`crypto/x509` and `crypto/tls` support. No new dependency, same as ML-KEM.

## 1. The threat is NOT symmetric with ML-KEM — read this before planning work

ML-KEM was urgent because of **Harvest Now, Decrypt Later**: an adversary
records ciphertext today and decrypts it once a cryptographically relevant
quantum computer (CRQC) exists. The traffic is already captured; the clock is
already running.

**Signatures have no HNDL analogue.** A signature forged in 2035 cannot make a
2026 peer accept it — that peer already made its decision with the key material
of its day. Forgery is only useful *at the moment of verification*. So the
deadline for PQ signatures is "before CRQCs exist", not "before an adversary
starts recording", and there is no accruing damage in the meantime.

This matters because the cost here is much higher than it was for ML-KEM
(§2), and the benefit does not compound while we wait. **Recommendation: this
is worth designing now and shipping deliberately, not urgently.**

One genuine exception worth separating out: **long-lived signed artifacts that
will still be trusted after a CRQC exists.** A discovery entry re-signed every
few minutes is not one. A genesis record, a release-signing key, or anything
whose signature is checked years after issuance is.

## 2. The cost is dominated by size, and size is already a live constraint

Measured on the installed Go 1.27 toolchain, not quoted from a spec:

| | secp256k1 (today) | ML-DSA-44 | ML-DSA-65 |
|---|---|---|---|
| signature | **65 B** | 2420 B (**37x**) | 3309 B (**51x**) |
| public key | **33 B** | 1312 B (**40x**) | 1952 B (**59x**) |

Nine structures in-tree carry a `json:"signature"` field. The two that matter:

- `pkg/dmsg/disc/entry.go` — every client and server discovery entry. ~981
  client entries observed live; +2420 B each is ~2.4 MB of discovery store.
  Tolerable.
- `pkg/transport/entry.go` — **this is the problem.** A visor publishes its
  whole transport list as ONE CXO snapshot leaf. A visor carrying 1000
  transports (routinely observed) would add ~2.4 MB of signatures to a single
  leaf, against a **16 MB `MaxObjectSize`** — a ceiling that already took down
  an entire feed in production when tpd-metrics exceeded it. The `CompactEntry`
  form exists *precisely because* the full form "wastes ~62% of the leaf on
  redundancy". Adding a 37x signature to each entry runs directly against work
  already done to make that leaf fit.

Also note `pkg/skychat/group/{gossip,moderation,keyrotate}.go` sign
per-message. A 37x signature on a chat gossip path is a different product.

## 3. Identity cannot move, so this is additive not substitutive

A skywire public key **is** the node's address: `cipher.PubKey [33]byte`,
secp256k1, used for routing, dmsg addressing, whitelists, rewards and the
`<pk>.dmsg` namespace. ML-DSA keys are 1312 B and cannot become addresses
without changing addressing everywhere.

Therefore ML-DSA is necessarily a **second** signature alongside the secp256k1
one, not a replacement — which compounds §2 rather than trading against it.
Hybrid signing here means "carry both", where hybrid key agreement meant "mix
both secrets" at no wire cost beyond one KEM exchange.

## 4. Scope: sign what is identity-critical, not everything

Applying ML-DSA uniformly is not viable (§2). Proposed tiers:

**Tier A — dual-sign (secp256k1 + ML-DSA).**
Long-lived identity assertions where forgery has lasting consequence and volume
is low: the visor's own discovery entry, and any release/genesis artifact.

**Tier B — leave classical.**
High-volume or short-lived: transport entries inside the CXO snapshot, skychat
gossip, per-request nonces. These are re-issued constantly, verified
immediately, and a forged one buys an attacker a single transient action that
the next snapshot corrects. The size cost is not justified by the threat.

**Tier C — reconsider later.**
Route-setup requests. Currently short-lived and high-volume (Tier B by that
logic), but they authorize rule installation on third-party visors, so a
forgery has more reach than the others. Decide with measurement, not now.

Being explicit that Tier B stays classical is the load-bearing decision in this
document. Without it, "add PQ signatures" silently means "add 2.4 MB to a leaf
that must stay under 16 MB".

## 5. Downgrade safety and negotiation — reuse the ML-KEM shape

The handshake design already solved this; follow it rather than invent.

- Advertise ML-DSA capability as a **signed field in the discovery entry**,
  exactly as the PQ-hybrid design does for ML-KEM. A capability claim that is
  itself classically signed is sufficient during migration: an attacker who can
  forge secp256k1 has already won without touching this.
- A verifier that knows (via signed discovery) that a peer is ML-DSA-capable
  **requires** the ML-DSA signature and fails closed on absence. That closes the
  strip-the-PQ-signature downgrade.
- A peer with no advertised capability is an honest old version, not an attack.

## 6. Rollout — the four phases from the handshake design

1. **Phase 0 — capability ship.** Release verifies ML-DSA and advertises
   support in its signed discovery entry. Signs nothing new. No wire change
   beyond one optional field.
2. **Phase 1 — dual-sign Tier A, opt-in.** Capable visors attach the second
   signature; verifiers check it when present, accept absence.
3. **Phase 2 — require between known-capable peers.** Once signed discovery
   says both sides support it, absence is a failure. Closes the downgrade tail.
4. **Phase 3 — mandatory**, gated on the uptime-tracker version census showing
   the fleet floor is capable — the same gate the handshake design uses.

Phases 2 and 3 must be gated on measured fleet version distribution, not
elapsed time. Today's session is a reminder of why: merged fixes reach the
fleet only through a release, so "shipped" and "deployed" differ by weeks.

## 7. Implementation notes

- Stdlib only: `crypto/mldsa`. Matches the ML-KEM precedent of adding no
  dependency.
- Keep the crypto in a self-contained file with no live call sites first, as
  `pkg/dmsg/noise/pqhybrid.go` did — reviewable in isolation before wiring.
- The ML-DSA key must be **derived deterministically from the existing secret
  key**, so a node does not acquire a second independent secret to back up, and
  an existing identity gains PQ capability without re-keying.

  The stdlib supports this directly: `mldsa.NewPrivateKey(params, seed)` takes a
  `mldsa.PrivateKeySize` = **32-byte seed**, which is exactly the shape of an
  HKDF output. So the derivation is
  `HKDF(existing SK, "skywire-mldsa-v1") -> 32 B -> NewPrivateKey`, mirroring
  `DeriveChildKey` in `pkg/cipher` and requiring no hand-rolled key handling.
  Verified: two `NewPrivateKey` calls with the same seed produce identical
  public keys.

  The relevant API is `mldsa.MLDSA44()` / `MLDSA65()` / `MLDSA87()` returning
  `Parameters`, then `GenerateKey(params)` or `NewPrivateKey(params, seed)`.

- **FIPS 140-3 caveat**, from the package docs: `crypto/mldsa` "is unavailable
  if using the FIPS 140-3 Go Cryptographic Module v1.0.0, in which case
  GenerateKey, NewPrivateKey, NewPublicKey, and Verify will return an error. It
  is available if using v1.26.0 or later." Any FIPS-mode build must therefore
  handle these returning errors rather than assuming availability — which is a
  concrete reason Phase 0 must *verify* before anything *requires*.
- Tests: FIPS-204 known-answer vectors; dual-signature round-trip; **downgrade
  rejection** (stripped ML-DSA signature must fail when the peer is known
  capable); mixed-version interop; and a size-regression test asserting the
  CXO transport-list leaf stays under `MaxObjectSize` with signatures present.

## 8. Open questions

- Does the discovery entry schema have room for a second signature and a
  capability flag without breaking older parsers? (The PQ-hybrid doc raises the
  same question and marks it unconfirmed.)
- ML-DSA-44 vs 65: 44 is the smaller and is FIPS-approved; 65 is the more
  conservative. Given §2 is size-dominated, 44 unless there is a reason.
- Is there any artifact in the tree whose signature is verified more than a year
  after issuance? If yes it is Tier A regardless of volume, and it is the real
  motivation for this work.
