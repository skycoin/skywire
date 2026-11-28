# Release signatures

Every checksum file a release publishes has a signature beside it:

- `SHA256SUMS.sig` on the rolling `develop-latest` pre-release
- `checksums.txt.sig` on tagged releases, plus `checksums.txt.offline.sig`

A signature is a hybrid of Ed25519 and ML-DSA-65 (FIPS 204). It verifies
only when both halves do, so it stays valid while either scheme is unbroken.
The public keys are built into the binary from `pkg/releasesig/keys`.

| key | where the private key lives | signs |
|---|---|---|
| `ci-2026` | the `RELEASE_SIGNING_KEY` Actions secret | every build, in CI |
| `offline-2026` | the release operator's machine | tagged releases, after CI |

## Verify a download

```
skywire cli release verify SHA256SUMS
sha256sum -c --ignore-missing SHA256SUMS
```

`verify` prints the name of the key that signed and exits non-zero when the
signature is missing, from an unknown key, or does not match. Pass `-s` to
check `checksums.txt.offline.sig`.

The binary auto-updater (`skywire-update`) checks `SHA256SUMS.sig` with the
installed `skywire` before it trusts `SHA256SUMS`.

## Sign a tagged release offline

After the release workflow finishes:

```
gh release download vX.Y.Z --repo skycoin/skywire --pattern 'checksums.txt*'
skywire cli release verify checksums.txt
skywire cli release sign -k ~/.config/skywire-release/offline-2026.key -o checksums.txt.offline.sig checksums.txt
gh release upload vX.Y.Z checksums.txt.offline.sig --repo skycoin/skywire
```

## Rotate a key

`skywire cli release keygen -n NAME -o FILE` writes a private key and prints
its public key. Add the public key to `pkg/releasesig/keys`, release, and
only remove the old key once the fleet runs a version that has the new one.
