# Skywire Auto-Update

Skywire uses a rolling-release auto-update mechanism. The CI pipeline tracks the latest commit on `develop` for which all tests passed, and visors can update themselves using standard Go tooling.

## How It Works

### CI Pipeline

1. A PR is merged to `develop`
2. The `Test` workflow runs on all 3 platforms (linux, darwin, windows)
3. If ALL tests pass, the `update-commit` job:
   - Updates the `skywire-commit` branch with the tested commit SHA
   - Warms the Go module proxy cache so the commit is immediately available worldwide
4. The `Deploy` workflow (independent) builds and pushes Docker images to Docker Hub, tagged with both `:test` and `:<short-sha>`

### The `skywire-commit` Branch

An orphan branch in `skycoin/skywire` containing a single Go program that prints the latest tested commit hash:

```
cmd/skywire-commit/main.go   # const Commit = "<sha>"
go.mod                        # module github.com/skycoin/skywire
```

This branch has no shared history with `develop` — it's updated by CI only.

## Visor Auto-Update

### Prerequisites

- Go 1.21+ installed (Go 1.23 from bookworm-backports works; Go auto-downloads the required toolchain)
- `~/go/bin` in `$PATH`

### Get the Latest Tested Commit

Using `go run` with isolated cache (no persistent build artifacts):

```bash
TMPDIR=$(mktemp -d) && COMMIT=$(GOCACHE=$TMPDIR go run github.com/skycoin/skywire/cmd/skywire-commit@skywire-commit) && rm -rf $TMPDIR
echo $COMMIT
```

Or install the helper binary (2MB, reusable):

```bash
go install github.com/skycoin/skywire/cmd/skywire-commit@skywire-commit
COMMIT=$(~/go/bin/skywire-commit)
```

### Install Skywire at the Tested Commit

```bash
go install github.com/skycoin/skywire@$COMMIT
```

### One-Liner (isolated, no persistent build artifacts from the commit check)

```bash
TMPDIR=$(mktemp -d) && go install github.com/skycoin/skywire@$(GOCACHE=$TMPDIR go run github.com/skycoin/skywire/cmd/skywire-commit@skywire-commit && rm -rf $TMPDIR)
```

### Check if Update is Needed

Compare the installed version to the latest tested commit:

```bash
CURRENT=$(skywire -b 2>/dev/null | grep -oP 'v.*-\K[a-f0-9]+' || echo "none")
LATEST=$(~/go/bin/skywire-commit)
if [ "${LATEST:0:12}" != "$CURRENT" ]; then
    echo "Update available: $CURRENT -> ${LATEST:0:12}"
fi
```

## Deployment Server Auto-Update (Docker)

Docker images are pushed to Docker Hub on every merge to `develop`, tagged with both `:test` and `:<short-sha>`. The `skywire-commit` hash identifies which image is verified by CI.

### Update Script

```bash
#!/bin/bash
# Get the latest tested commit
go install github.com/skycoin/skywire/cmd/skywire-commit@skywire-commit
LATEST=$(~/go/bin/skywire-commit)
SHORT=${LATEST:0:12}
CURRENT=$(cat ~/.skywire-deploy-commit 2>/dev/null || echo "none")

if [ "$SHORT" = "$CURRENT" ]; then
    echo "Up to date at $SHORT"
    exit 0
fi

echo "Updating from $CURRENT to $SHORT"
docker pull skycoin/skywire:$SHORT
cd /path/to/docker-compose && docker compose up -d --force-recreate
echo "$SHORT" > ~/.skywire-deploy-commit
```

## Go Module Proxy and China

The CI warms the Go module proxy (`proxy.golang.org`) after each successful test run, so visors don't need `GOPROXY=direct`. The Chinese mirror `goproxy.cn` mirrors `proxy.golang.org`, so Chinese visors work with the default Go proxy settings.

## Build Cache Management

The Go build cache (`~/.cache/go-build`) grows over time. Periodic cleanup:

```bash
# Remove build cache (safe — rebuilds are just slower the first time)
go clean -cache

# Check cache size
du -sh ~/.cache/go-build
```

For the `go run` commit check, use the isolated `GOCACHE` pattern shown above to avoid cache growth entirely.
