# Export compliance

App Store Connect asks about encryption for every build until the answers
are recorded in Info.plist. This file holds the facts those answers rest on,
read from the code and the iOS core's import graph, and the answers we
propose. The answers are the account holder's call (**Decision**); export
law is not something this repository can settle.

## What encryption the app contains

The iOS core is the Go lite core (`cmd/skywire-mobile-core`, the graph
`make ios-deps-check` prints). The app around it is Swift.

| Where | What | Algorithms | Purpose |
|---|---|---|---|
| dmsg sessions and streams (`pkg/dmsg`) | Noise XK and KK handshakes (github.com/flynn/noise) | ECDH on secp256k1, ChaCha20-Poly1305, SHA-256 | confidentiality and authentication of every connection between nodes, end to end between the two nodes for a stream |
| transports between nodes (`pkg/transport`) | Noise KK, and TLS 1.3 for QUIC and WebTransport (quic-go), DTLS for WebRTC transports (pion) | as above, plus the TLS 1.3 suites (AES-GCM, ChaCha20-Poly1305, X25519 and Go's default hybrid with ML-KEM) | the same, over each transport kind |
| HTTPS to the network's services and the wallet's nodes | Go `crypto/tls`, not Apple's TLS | TLS 1.2 and 1.3 suites | confidentiality and server authentication |
| SkyChat messages (`pkg/skychat`) | per-conversation and group keys | ChaCha20-Poly1305, HKDF-SHA256, AES | end-to-end confidentiality, forward secrecy |
| calls | voice over the dmsg stream above; SRTP is linked (pion) | as above | confidentiality |
| config at rest, when the user turns on Encrypt config | CryptoKit (Apple) | AES-GCM | data at rest |
| passwords and the visor key | iOS Keychain (Apple) | Apple's | data at rest |
| wallet | libsecp256k1 (vendored, bitcoin-core v0.8.0), SHA-256, RIPEMD-160, Keccak-256, BIP-32 HMAC-SHA512, BIP-39 PBKDF2 | secp256k1 ECDSA signatures, hashes | signing transactions and deriving keys; no data is encrypted with it |

Linked but not used for the app's own traffic: Go's standard library and
golang.org/x/crypto also carry DES, RC4, Blowfish, CAST5, Twofish, TEA and
SSH code through dependencies. Nothing in the app selects them.

Every encryption algorithm above is a published standard (SEC 2 for secp256k1, RFC 8439
ChaCha20-Poly1305, RFC 5869 HKDF, RFC 8446 TLS 1.3, RFC 6347/9147 DTLS, RFC
3711 SRTP, FIPS 197 AES, FIPS 180-4 SHA-2, FIPS 203 ML-KEM). Noise is an
openly published protocol framework (noiseprotocol.org) rather than a
standards-body document; its use of secp256k1 instead of the spec's
Curve25519 is Skywire's own choice. The source of all of it is public
(AGPL-3.0, https://github.com/skycoin/skywire).

## Proposed answers

1. *Does your app use encryption?* **Yes.**
2. *Which algorithms?* **Standard encryption algorithms instead of, or in
   addition to, the encryption in Apple's operating system.** The app does
   its own TLS, Noise and message encryption in Go. **Decision:** whether the
   Noise-with-secp256k1 composition should be answered as non-standard
   ("Both"). Its primitives are standard; the composition is not an IETF or
   ISO document.
3. *Exemptions.* None of the narrow exemptions (authentication only, copy
   protection, banking) covers it: the encryption protects the user's
   communications. Under the US EAR this reads as mass-market encryption
   software (5D992.c, License Exception ENC §740.17(b)(1)), which needs an
   annual self-classification report to BIS and the NSA. **Decision:** the
   account holder confirms the classification, files the report, and
   decides what the public source changes (EAR §734.3(b)(3), §742.15(b)).
4. *France.* If the app is offered in France, App Store Connect asks for the
   French encryption declaration. **Decision:** file it, or leave France out
   of the availability list.

## Once decided

Add to `ios/Skywire/Info.plist` (and the extension's) so builds stop asking:

```xml
<key>ITSAppUsesNonExemptEncryption</key>
<true/>
```

and, if Apple issues one, `ITSEncryptionExportComplianceCode` with its
code. Until then each upload waits for the questions in App Store Connect,
and external TestFlight testers cannot get the build before they are
answered.
