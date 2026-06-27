# Serving the standalone wasm-visor (auto-updating)

Serve the in-browser wasm-visor as a single keyless page on a subdomain, with the served
build kept current automatically as skywire updates on the host. No interactive
skywire-cli / test-harness access to the serving instance — just a long-running serve
process behind a reverse proxy.

## How it works

The skywire binary **embeds** both the wasm-visor (`pkg/wasmhv/wasmbin/wasm-visor.wasm.gz`,
the standard-Go build) and the hypervisor UI. So:

```
skywire cli hv serve --addr 127.0.0.1:7999
```

builds a **keyless, self-contained** page once at startup from whatever wasm + UI are in
the *current* binary, and serves it over HTTP. Because the page reflects the running
binary:

> **serve == one long-running process; "update" == restart it after the binary updates.**

Caddy reverse-proxies it on the public subdomain (auto-HTTPS). The host is never touched
with skywire-cli interactively.

## Keyless = safe to serve from a domain

With no key flags, the page bakes in **no key**. Each visitor's browser mints its own
ephemeral key and persists it in `localStorage` (via `hv-boot.js`), so refreshes keep the
same in-browser visor while picking up freshly served code. The page never asks anyone to
type a secret key — which is why serving it from a domain is fine. (The key-entry-spoof
risk only applies to key-BEARING `hv gen` files; never serve those from a domain.)

## Setup

```sh
# Install + start the serve process (edit the skywire path / port inside if needed)
sudo cp skywire-wasm-visor-serve.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now skywire-wasm-visor-serve.service

# Caddy: edit the subdomain in ./Caddyfile, include the block, reload
sudoedit /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

## Keeping it current on auto-update

The page is built at process start, so restart the serve service after skywire updates:

- **If your auto-updater restarts services** (the common case): add
  `skywire-wasm-visor-serve.service` to its restart list. Done.
- **Otherwise**, add a path-triggered restart:
  ```ini
  # /etc/systemd/system/skywire-wasm-visor-restart.service   (oneshot)
  [Service]
  Type=oneshot
  ExecStart=/usr/bin/systemctl restart skywire-wasm-visor-serve.service

  # /etc/systemd/system/skywire-wasm-visor-restart.path
  [Path]
  PathChanged=/usr/bin/skywire
  Unit=skywire-wasm-visor-restart.service
  [Install]
  WantedBy=multi-user.target
  ```
  `sudo systemctl enable --now skywire-wasm-visor-restart.path`.
  (A `.path` must trigger a oneshot *restart* — pointing it straight at the serve service
  would be a no-op, since the service is already running.)

## Notes

- Force a manual refresh of the served build: `sudo systemctl restart skywire-wasm-visor-serve.service`.
- For a viewer-only page or a key-bearing personal build, use `skywire cli hv gen`
  (`--viewer-pk`, `--sk`, `--password`) and open it from `file://` — do **not** serve a
  key-bearing build from a domain.
- Future: ship `skywire-wasm-visor-serve.service` with the skywire package so this is a
  one-liner enable.
