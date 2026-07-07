# Skychat Voice RFC — real-time voice, all media on the encrypted mesh

status: draft
date: 2026-07-07
related: docs/skychat-refactor-rfc.md, docs/skynet-routing-control-rfc.md
         (voice=WebRTC-first), pkg/transport/network/webrtc.go (dmsg-signaled
         WebRTC transport), pkg/router/datagram_* (faithful-UDP datagram route
         groups, RFC #2607), pkg/skychat

## 1. Goal & the hard constraint

Add real-time voice to skychat — 1:1 first, then group rooms — on **both** the
native visor and the browser (wasm) visor.

**Hard constraint (operator):** *all voice data travels over an encrypted skywire
transport or dmsg connection.* No media on a raw-internet path. That single rule
decides the whole design, because it rules out the thing WebRTC normally does.

## 2. Why vanilla WebRTC does not satisfy the constraint

skywire already has a **WebRTC transport** (`pkg/transport/network/webrtc.go`): it
signals the SDP offer/answer + ICE candidates over a dmsg stream
(`skyenv.DmsgWebRTCSignalPort = 56`), then the two peers open a DataChannel
(DTLS+SCTP) **NAT-traversed via ICE**. That works great as a *transport*, but the
media/DataChannel path is a **direct P2P connection over the public internet**
(ICE host/srflx candidates) — the two peers learn each other's IPs and the bytes
leave the mesh. For a *transport* that's an accepted trade (it's one of five
carriers); for **voice under this constraint it's disqualified**.

So we keep the *good* parts of WebRTC — the **Opus codec**, the RTP framing, the
jitter buffer, packet-loss concealment, and (in the browser) the acoustic echo
canceller — but we **replace WebRTC's transport (ICE/DTLS-SRTP) with a skywire
transport.** The audio bytes ride the mesh; WebRTC is reduced to a media library.

## 3. Architecture

```
  mic ─▶ Opus encode ─▶ RTP frame ─▶ [skywire media transport] ─▶ dejitter ─▶ Opus decode ─▶ speaker
        (native: pion / wasm: WebCodecs)         │
                                                 └── faithful-UDP datagram route (default)
                                                     or a dmsg stream (1:1 fallback)
  control:  call setup / teardown / codec params / keys  ── over dmsg (skychat control plane)
```

Three planes:

1. **Signaling / control** — over dmsg, reusing the existing pattern. Call
   setup (invite, accept, decline, hang-up), negotiated codec parameters, the
   media session id, and the per-call key are exchanged over a dmsg stream
   (the `DmsgWebRTCSignalPort` mechanism, or a dedicated skychat-voice control
   port), tied to the skychat DM/room the call belongs to. This is reliable and
   already IP-anonymous.

2. **Media transport** — the skywire mesh, encrypted, loss-tolerant:
   - **Default: the faithful-UDP datagram route group** (`pkg/router`, RFC
     #2607). Voice wants UDP semantics — drop late packets, never head-of-line
     block — and datagram routes give exactly that over skywire routes, already
     Noise-encrypted and IP-anonymous (multi-hop). RTP/Opus packets map 1:1 onto
     datagram-route packets.
   - **1:1 fallback: a dmsg stream.** When a direct/1-hop path exists and a
     datagram route isn't set up, a dmsg stream carries the same RTP frames
     (reliable-ordered adds a little latency, fine for a 1:1 call to bootstrap).
   Either way the carrier is an encrypted skywire transport — constraint met.

3. **Codec / media** — **Opus** (48 kHz, ~16–32 kbps for voice, built-in FEC +
   PLC). Native uses pion's Opus + RTP packages; the browser uses the native
   WebCodecs `AudioEncoder`/`AudioDecoder` (Opus). One RTP profile, one codec,
   both runtimes.

## 4. Native visor (pion as a media library, no ICE)

pion is already vendored (`github.com/pion/webrtc/v4`) and gives us everything
**except** the transport, which we don't want from it:

- **Capture / playback:** `pion/mediadevices` (miniaudio/portaudio) or
  `malgo` + `oto`. Mic → PCM; PCM → speaker.
- **Codec:** `pion/mediadevices/pkg/codec/opus` (or `hraban/opus`). PCM ⇄ Opus.
- **RTP + jitter:** `pion/rtp` for packetization; `pion/interceptor`'s jitter
  buffer + NACK/PLC on the receive side.
- **Transport:** write the RTP packets to the **datagram-route `net.PacketConn`**
  (or a dmsg stream `net.Conn`) — NOT a pion ICE/DTLS transport. No `SettingEngine`,
  no STUN/TURN, no `PeerConnection`. We use pion's media stack over our own conn.
- **Echo cancellation / noise suppression:** the genuinely hard native piece
  (the browser gets it free). Options, in order: ship 1:1 **without** AEC first
  (headset / push-to-talk users are fine), then add RNNoise for NS and a WebRTC
  APM binding for AEC. Keep all of this behind cgo build tags so js/wasm + TinyGo
  builds stay clean (KG6).

## 5. Wasm visor (browser)

The browser has a first-class audio stack — we use it **without** RTCPeerConnection
so the media rides the wasm visor's skywire transport, not ICE:

- **Capture:** `getUserMedia({audio:{echoCancellation:true, noiseSuppression:true,
  autoGainControl:true}})` → an `AudioWorklet` taps PCM frames.
- **Codec:** WebCodecs `AudioEncoder`/`AudioDecoder` with `'opus'` — the browser's
  own Opus, no wasm codec needed.
- **Transport:** send/receive the Opus frames over the wasm visor's existing mesh
  hooks — a dmsg stream (`skywireVisor.fetchDmsg`-style bidirectional stream) or,
  once wired, the datagram-route carrier. Same bytes as native.
- **Playback:** decoded PCM → `AudioWorklet`/`AudioBufferSourceNode` with a small
  jitter buffer.

**AEC trade-off (the one real wart):** `getUserMedia`'s `echoCancellation` runs
inside the browser's WebRTC audio pipeline; when we bypass `RTCPeerConnection` for
the media, that AEC does **not** automatically apply to WebCodecs-captured audio.
Two answers:
- **Phase 1 (simple):** rely on `echoCancellation:true` on the capture constraint
  (modern browsers apply APM to the raw `MediaStreamTrack` before it reaches an
  `AudioWorklet` in most engines) + headset/push-to-talk. Ship this.
- **Phase 2 (full browser AEC over our transport):** a loopback `RTCPeerConnection`
  purely for its APM + encoder, with **RTCRtpScriptTransform / Encoded Transform**
  to pull the *encoded* Opus frames out before they hit ICE and push them onto the
  skywire transport (and inject received frames into a decode-only peer connection
  the same way). Keeps browser-grade AEC while the bytes still ride the mesh. More
  complex; a follow-up, not the MVP.

## 6. Group voice

- **Mesh (small rooms, ≤ ~5):** each participant runs one media transport per
  other participant — a datagram route (or dmsg stream) per pair. Reuses the 1:1
  path N times. N² upload is the ceiling.
- **SFU on a well-connected member (larger rooms):** one node — the **room
  owner** (already the coordination point; it holds the roster/feed) or a
  volunteered high-bandwidth native member — receives everyone's RTP and forwards
  to all others. pion has the forwarding primitives. **Every hop is still a
  skywire transport**, so the constraint holds for the SFU topology too. The SFU
  forwards *encoded* Opus (no transcoding) to keep CPU low.
- Who-talks-to-whom is discovered from the **skychat room roster** (the CXO group
  feed — the same one the group-chat text uses); members then set up media
  transports pairwise or to the SFU.

## 7. Security

- The skywire transport (dmsg / datagram route) is **Noise-encrypted** end of the
  link — the constraint is met by construction; no plaintext on the wire, no
  raw-internet path.
- **Defense in depth (optional, recommended for the SFU case):** a per-call
  symmetric key negotiated in the dmsg signaling, used to **encrypt the RTP
  payload end-to-end** (SRTP-style) so an SFU/relay forwards **ciphertext** and
  can't listen in. For 1:1 mesh this is redundant with the transport encryption;
  for the SFU it matters.
- No public STUN/TURN, ever (that would leak IPs and defeat the constraint). If
  NAT traversal is needed it is dmsg's job, not ICE's.

## 8. Integration with skychat

Voice is a **call inside a skychat conversation** — a 1:1 DM or a room:
- Signaling piggybacks on the skychat control plane (the same dmsg surface that
  carries pairing / group control), tagged with the conversation id.
- The UI adds a call button to the DM/room (native Angular tab + the wasm desktop
  chat window), a ringing/accept/decline state, and mute / push-to-talk.
- Presence ("in a call") is surfaced on the roster the room already publishes.

## 9. Phasing

1. **1:1 native↔native** — Opus/RTP over a **dmsg stream** first (simplest
   carrier), pion media stack, no ICE. Proves the media path end-to-end.
2. **Swap the 1:1 carrier to the datagram route** — loss-tolerant, lower latency,
   multi-hop-anonymous.
3. **1:1 wasm↔native** — WebCodecs + Web Audio over the wasm dmsg/datagram
   transport (Phase-1 AEC).
4. **Group mesh** (small rooms).
5. **SFU on the owner** + optional E2E SRTP key.
6. **Browser Encoded-Transform AEC** (Phase-2 wart fix), native APM/RNNoise.

Each step is independently shippable and leaves skychat working.

## 10. Open questions

- Datagram-route setup latency for call start — pre-warm a route when a DM is
  opened, or set it up on call-accept? (affects "time to first audio").
- 1:1: prefer a direct 1-hop datagram route (lowest latency) vs a multi-hop route
  (IP-anonymous) — per-call policy flag, mirroring skynet-routing-control's
  direct-vs-mesh toggle.
- SFU election for group rooms — owner by default; how to fail over / volunteer a
  better-connected member.
- Jitter-buffer target depth vs latency for mesh voice (routes have higher, more
  variable RTT than internet WebRTC) — adaptive, seeded from the route's measured
  RTT.
- Opus bitrate / DTX (discontinuous transmission on silence) defaults for the
  mesh's bandwidth budget.
