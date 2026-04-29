# Skywire Chat app

Chat implements basic text messaging between skywire visors.

Messaging UI is exposed via web interface.

Chat only supports one WEB client user at a time.

## Local setup

Create 2 visor config files:

`skywire1.json`

```json
{
  "apps": [
    {
      "app": "skychat",
      "version": "1.0",
      "auto_start": true,
      "port": 1
    }
  ]
}
```

`skywire2.json`

```json
{
  "apps": [
    {
      "app": "skychat",
      "version": "1.0",
      "auto_start": true,
      "port": 1,
      "args": ["-addr", ":8002"]
    }
  ]
}
```

Compile binaries and start 2 visors:

```bash
$ go build -o ./build/apps/skychat.v1.0 ./cmd/apps/skychat
$ cd ./build
$ ./skywire-visor skywire1.json
$ ./skywire-visor skywire2.json
```

Chat interface will be available on ports `8001` and `8002`.

## Per-pair private messaging (CXO)

Skychat supports an opt-in pairing layer that mounts each
conversation on its own CXO TreeStore feed with end-to-end
ECDH+ChaCha20-Poly1305 encryption and a publisher-side allowlist.
Compared to the legacy direct path, this gives:

- Content privacy (subscribers see only ciphertext; bodies are
  AEAD-sealed with an ECDH-derived key per pair).
- Read-access control (only the partner's PK can subscribe).
- Offline delivery (CXO replication catches the peer up when it
  comes back online).

Enable via flags:

```
skychat --pair-enable --pair-rpc localhost:3435
```

When enabled, the UI shows a "Pending Pair Invites" section above
the contact list and a pair toggle button in the chat header.
Incoming invites are consent-based — they wait for an explicit
Accept or Decline.

See [docs/skychat_pairing.md](../../../docs/skychat_pairing.md)
for the full design, threat model, HTTP/SSE schema, and
operational details.
