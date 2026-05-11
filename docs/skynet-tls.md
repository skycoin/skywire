# Resolver TLS MITM mode

The `skywire cli resolver` proxy can optionally terminate TLS for
`*.skynet` and `*.dmsg` browser requests, presenting a leaf cert
signed by a locally-installed CA. This satisfies the browser's
secure-context requirements (Stripe.js, Payment Request API,
service workers, getUserMedia, etc.) without going through any
public certificate authority.

The CA is name-constrained via X.509 `nameConstraints` to *only*
issue certs for `.skynet` and `.dmsg`. Even though it is installed
in the OS trust store, a stolen or buggy CA key cannot mint valid
certs for the rest of the public web.

The cross-wire authentication is provided by the underlying skywire
transport (Noise + secp256k1 visor pubkey). The locally-issued leaf
cert exists only to satisfy browser machinery; it is not the trust
anchor for the connection.

## Status

* **Linux desktop browsers (Chrome/Chromium/Firefox):** supported.
* **macOS / Windows:** generation and configuration work, but the
  `install` subcommand is Linux-only. Install the cert manually
  per the platform sections below.
* **iOS:** supported via a `.mobileconfig` profile (manual; not
  scripted yet).
* **Android:** Chrome ignores user-installed CAs since Android 7.
  Firefox-for-Android can use it via its own NSS store.

## First-time setup (visitor side)

```sh
# 1. Generate the local CA. Lives at ~/.skywire/resolver/ca.{crt,key};
#    the key file is 0600.
skywire cli resolver ca gen

# 2. Add to the OS system trust store (Linux only at the moment).
skywire cli resolver ca install

# Or print the install commands without running them, e.g. to
# vet what's about to happen or run them yourself:
skywire cli resolver ca install --print

# 3. (Firefox only) add the CA to Firefox's separate NSS store.
#    See "Firefox" below.

# 4. Print fingerprint when needed.
skywire cli resolver ca fingerprint
```

## Enable in the visor config

Edit `skywire-config.json` and set `tls_mitm: true` plus the CA
paths under one or both resolvers:

```json
"skynet_web": {
  "enable": true,
  "tls_mitm": true,
  "tls_ca_path": "/home/USER/.skywire/resolver/ca.crt",
  "tls_ca_key_path": "/home/USER/.skywire/resolver/ca.key"
},
"dmsg_web": {
  "enable": true,
  "tls_mitm": true,
  "tls_ca_path": "/home/USER/.skywire/resolver/ca.crt",
  "tls_ca_key_path": "/home/USER/.skywire/resolver/ca.key"
}
```

Restart the visor. On startup it logs:

```
INFO ... skynetweb TLS MITM enabled  ca_fingerprint=...
```

If the CA cannot be loaded, the visor logs a warning and the
resolver continues running in plain (non-MITM) mode.

## Manual install per platform

`skywire cli resolver ca install --print` shows the commands for
your distro. The general procedure:

### Arch / Manjaro

```sh
sudo install -m 0644 ~/.skywire/resolver/ca.crt /etc/ca-certificates/trust-source/anchors/skywire-resolver.crt
sudo trust extract-compat
```

### Debian / Ubuntu

```sh
sudo install -m 0644 ~/.skywire/resolver/ca.crt /usr/local/share/ca-certificates/skywire-resolver.crt
sudo update-ca-certificates
```

### Fedora / RHEL / CentOS

```sh
sudo install -m 0644 ~/.skywire/resolver/ca.crt /etc/pki/ca-trust/source/anchors/skywire-resolver.crt
sudo update-ca-trust
```

### Firefox (any platform)

Firefox does not consult the system trust store by default. Add
the CA to each profile's NSS database:

```sh
# Find the profile dir
ls ~/.mozilla/firefox/

# Add (replace PROFILE with the actual dir name)
certutil -A -d sql:$HOME/.mozilla/firefox/PROFILE -t "CT,," -n "skywire-resolver" -i ~/.skywire/resolver/ca.crt
```

`certutil` ships in the `nss` (Arch), `libnss3-tools` (Debian/Ubuntu),
or `nss-tools` (Fedora) packages.

### macOS (manual; not yet scripted)

```sh
sudo security add-trusted-cert -d -r trustRoot \
  -k /Library/Keychains/System.keychain \
  ~/.skywire/resolver/ca.crt
```

### Windows (manual; not yet scripted)

In an admin PowerShell:

```powershell
certutil -addstore -f Root $HOME\.skywire\resolver\ca.crt
```

## Uninstall

```sh
skywire cli resolver ca uninstall
```

Removes `skywire-resolver.crt` from the system anchors directory
and runs the distro update command. The on-disk CA at
`~/.skywire/resolver/ca.{crt,key}` is left in place; delete it
manually if you want to revoke completely.

## Verifying the install

After install + visor restart, point a browser at the resolver
SOCKS5 (default `127.0.0.1:4446` for skynet, `4445` for dmsg) and
visit `https://<visor-pk-dns-label>.skynet/`. You should see the
merchant's content with no certificate warning. Browser dev-tools
should report `isSecureContext = true` and the cert issuer as the
local "Skynet Resolver Local CA".

### Why `<visor-pk-dns-label>` and not hex

A 33-byte PubKey is 66 hex chars, which overflows two strict
limits enforced by NSS (Firefox / Waterfox / Chrome):

  - RFC 1035 caps each DNS label at 63 octets.
  - X.520 caps an X.509 Subject `CN` attribute at 64 chars
    (ub-common-name).

A leaf cert minted for `<66-hex>.skynet` is rejected as
"improperly encoded" before any trust check runs. The base32 form
produced by `<pk>.DNSLabel()` is 53 chars — fits both limits.

The hex form is still accepted by the resolver for backwards
compatibility with plain-HTTP URLs already in circulation, but
HTTPS will not work with it regardless of CA install state.

To get a visor's PK in the URL form, use:

```
skywire cli pk dnslabel                       # local visor
skywire cli pk dnslabel <hex-pk>              # arbitrary PK (hex → base32)
skywire cli pk dnslabel <base32-pk>           # round-trip back to hex
```

## Trust scope

The installed CA can sign certs only for hostnames ending in
`.skynet` or `.dmsg`. This is enforced by:

1. The `nameConstraints` extension on the CA cert itself (RFC 5280
   §4.2.1.10). Any leaf signed for a forbidden domain fails
   X.509 validation in conforming verifiers (Chromium, Firefox,
   recent Safari, Go's `crypto/x509`).
2. The `permittedSuffixes` check in `pkg/skynetca` itself —
   defense-in-depth so the runtime never even mints a forbidden
   leaf, regardless of what a malicious caller asks for.

A compromised resolver CA private key cannot be used to MITM any
public-internet HTTPS site. It can only impersonate any
`<pubkey>.skynet` or `<pubkey>.dmsg` host *to your browser*. Since
the visor's underlying skywire transport authenticates the peer
visor by pubkey independently, a malicious actor would also need to
hijack the skywire transport to actually serve content; the leaf
cert alone gets them nothing.
