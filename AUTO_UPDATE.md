# Skywire Auto-Update

Skywire uses a rolling-release auto-update mechanism. Visors track the tip of
`develop` (or a tagged release, a pinned commit, or a prebuilt binary) and update
themselves either by rebuilding from source with standard Go tooling or by
downloading a verified prebuilt binary.

## Update Channels

The auto-updater (`skywire-update`, shipped by the `skywire-autoupdate` AUR
package and the apt `.deb`) resolves its target from `UPDATE_CHANNEL` in
`/etc/skywire.conf`:

| `UPDATE_CHANNEL` | Resolves to |
|------------------|-------------|
| `develop` (default) | the tip of the `develop` branch — `go install github.com/skycoin/skywire@develop` |
| `latest` | the latest tagged release |
| `<commit-hash>` | a specific pinned commit |
| `binary` / `binary-develop` / `binary-master` | download the prebuilt linux binary for this arch from the rolling `<branch>-latest` GitHub pre-release, verified against `SHA256SUMS` |

The `develop`, `latest`, and `<commit-hash>` channels build from source. The
`binary*` channels download a prebuilt, zstd-compressed linux binary instead of
compiling — useful on hosts without a working Go toolchain or enough disk/CPU to
build.

## Source Builds

For the source channels, `skywire-update` installs the target with
`go install github.com/skycoin/skywire@<ref>`:

```bash
go install github.com/skycoin/skywire@develop        # UPDATE_CHANNEL=develop
go install github.com/skycoin/skywire@<commit-hash>  # UPDATE_CHANNEL=<commit-hash>
```

### GOPROXY

Source builds try `GOPROXY=direct` first (fetching module contents straight from
git) and automatically fall back to the default module proxy
(`proxy.golang.org`) if the direct git fetch fails. Set `GOPROXY_MODE` in
`/etc/skywire.conf` to force one mode:

- `GOPROXY_MODE=direct` — only ever fetch direct from git.
- `GOPROXY_MODE=proxy` — only ever use the default module proxy.

The Chinese mirror `goproxy.cn` mirrors `proxy.golang.org`, so Chinese visors
work with the default (`proxy`) fallback.

## Prebuilt Binaries

`.github/workflows/publish-binary.yml` builds a compressed linux binary
(amd64, arm64, armv7) on every merge to `develop` and `master` and publishes it
to a single rolling GitHub **pre-release** tagged `<branch>-latest` (e.g.
`develop-latest`), replaced in place on each merge. Each release ships a
`SHA256SUMS` file; the `binary*` channels download the matching-arch archive and
verify it against those checksums before installing.

This is deliberately not a tagged semver release — the rolling tag and
`prerelease=true` keep it out of the packaging/versioning flow (AUR/apt/MSI),
which key off real `vX.Y.Z` tags.

## Deployment Server Auto-Update (Docker)

Docker images are pushed to Docker Hub on every merge to `develop`, tagged with
both `:test` and `:<short-sha>`. Container deployments auto-pull the image and
recreate the compose stack when it changes.

## Build Cache Management

Source builds grow the Go build cache (`~/.cache/go-build`) and module cache
over time. `skywire-update` prunes these on exit; `MODCACHE_CAP_MB` in
`/etc/skywire.conf` caps the module cache. To clean manually:

```bash
go clean -cache      # build cache (safe — rebuilds are just slower the first time)
du -sh ~/.cache/go-build
```
