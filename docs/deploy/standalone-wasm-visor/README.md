# Serving the standalone wasm-visor (auto-updating)

Serve the in-browser wasm-visor as a single static HTML file on a subdomain, with
the served file automatically refreshed every time skywire updates on the host.

## How the auto-update works

The skywire binary **embeds** both the wasm-visor (`pkg/wasmhv/wasmbin/wasm-visor.wasm.gz`,
the standard-Go build) and the hypervisor UI. So:

```
skywire cli hv gen -o index.html
```

emits a **keyless, self-contained** `index.html` built from whatever wasm + UI are
in the *current* binary — no TinyGo, no node, no network, no skywire RPC. When the
binary updates, regenerating picks up the new wasm + UI. That's the whole trick:

> **serve == a plain static file; "update" == regenerate when the binary changes.**

The host never needs to be touched with skywire-cli interactively or via the test
harness — a `systemd .path` unit watches the binary and re-runs `hv gen` on change.

## Keyless = safe to serve from a domain

With no `--sk`/`-c`/`--password`, the file bakes in **no key**. Each visitor's
browser mints its own ephemeral key and persists it in `localStorage` (via
`hv-boot.js`), so refreshes keep the same in-browser visor while picking up freshly
served code. The page never asks anyone to type a secret key — which is exactly why
serving it from a domain is fine (the key-entry-spoof risk only applies to
key-BEARING generated files; never serve those from a domain).

## Setup

```sh
sudo mkdir -p /var/www/wasm-visor

# Install the units (adjust the skywire path inside them if not /usr/bin/skywire)
sudo cp skywire-wasm-visor-regen.service /etc/systemd/system/
sudo cp skywire-wasm-visor-regen.path    /etc/systemd/system/
sudo systemctl daemon-reload

# Generate once now, and re-generate automatically on every skywire update
sudo systemctl start  skywire-wasm-visor-regen.service
sudo systemctl enable --now skywire-wasm-visor-regen.path

# Caddy: drop the Caddyfile block in (edit the subdomain first), then reload
sudoedit /etc/caddy/Caddyfile     # paste/include the block from ./Caddyfile
sudo systemctl reload caddy
```

Visit `https://<your-subdomain>/` — Caddy serves the ~16 MB HTML (gzipped to ~5 MB),
the wasm-visor boots in the tab, registers in dmsg-discovery, and shows its own
hypervisor UI.

## Notes

- The big file is served once and cached (`Cache-Control: max-age=300`); a refresh
  after a skywire update pulls the new build.
- To force a regen by hand: `sudo systemctl start skywire-wasm-visor-regen.service`.
- If you want a viewer-only page (no in-tab visor) or a key-bearing personal build,
  see `skywire cli hv gen --help` (`--viewer-pk`, `--sk`, `--password`) — but do
  **not** serve a key-bearing build from a domain.
