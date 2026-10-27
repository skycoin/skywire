---
name: skywire-devflow
description: Build, dev loop, test, TinyGo native build and trial, and PR workflow for the skywire repository. Load before building, testing, committing or shipping skywire changes.
---

# skywire dev, build and PR workflow

Read AGENTS.md in the repository root first. It sets the commit, PR and writing rules.

## Build
- `go build .` at the repository root builds the one binary that holds the visor, the CLI and every app (`skywire app <name>`). Never `go build ./...`.
- If the temp directory is small, point `TMPDIR` and `GOTMPDIR` at a directory with space before a large build.
- Put scratch binaries outside the source tree, for example `go build -o /path/to/scratch/skywire .`.
- `make build-ui` builds the hypervisor UI. `make format` fixes import order and formatting. `make build-wasm` compile checks the js/wasm module. `make test-wasm` runs the js/wasm tests under Node.
- `make lint` needs `make install-linters` first. If `golangci-lint` is not found, add `$(go env GOPATH)/bin` to `PATH`.

## Test
- `make check` runs the linters, vet, the config generation and help smoke checks, and the tests. It serializes itself with a lock file. It starts with a long compile phase that prints nothing.
- For a targeted run use `go test ./pkg/foo/ -run TestX -count=1`.
- The skills in `.claude/skills` are checked by `go test ./cmd/skywire/commands/ -run TestSkillsMatchCommandTree`.

## Dev loop
- `scripts/dev-visor-loop.sh` rebuilds the working tree and restarts the visor each time it exits. Deploy a change with `skywire cli visor halt` and let the loop restart it. Do not kill the visor or relaunch it by hand.
- Write a binary path to `./local/visor-binary` and the loop runs that binary instead of building. Delete the file to build from source again.
- A dirty build reports the commit hash plus `+dirty`, and the build date is the commit time. Judge whether a rebuild landed by behavior or process start time.

## TinyGo native build
The visor also builds as a native TinyGo binary. Use the fork `github.com/0magnet/tinygo`, which carries the runtime and reflect fixes the visor needs. Stock TinyGo works for the small wasm modules in `docs/examples`, not for the visor.

```
tinygo build -scheduler=cores -gc=boehm -stack-size=1MB -no-debug -o skywire-tg .
```

- `TINYGO_LTO_OPT=0` shortens the build a lot at the cost of binary size.
- The build needs a big machine. Do it on a host with plenty of memory and copy the binary over. Run it under `systemd-run` or similar so memory pressure cannot take the desktop down.
- `scripts/tinygo-toolchain.sh` prints the `GOTOOLCHAIN` value TinyGo needs when it lags the system Go.

## Guarded trial of a TinyGo binary on a real visor
Never leave an untested TinyGo build running unattended. The pattern is:
1. Pause the auto updater so it does not replace the binary.
2. Swap the TinyGo binary in, keeping the Go binary beside it.
3. Start a watchdog that restores the Go binary and restarts the service when health fails, memory (RSS) or CPU stays over a limit, or a time limit passes.
4. Arm a dead-man timer (for example a transient systemd timer) that restores the Go binary even if the watchdog dies.
5. Watch `skywire cli visor state` and profile with `perf` and `gdb` when CPU is high.
6. When the binary on disk changed after the process started, resolve `perf` addresses with `nm` on the binary that is actually running.
7. Restore the Go binary and resume the updater when done.

## PR and merge
- Branch from `upstream/develop` and push the branch to your fork. Open the PR against `skycoin/skywire` `develop` with `gh pr create --repo skycoin/skywire --base develop --head <fork>:<branch>`.
- Never push to `develop` directly. Stage specific files, never `git add -A`.
- Commit subjects are `package: lowercase summary`. No AI attribution trailer in commits or PR bodies.
- Merging to `develop` deploys. Fleet visors on the update channel pull it within minutes, so test anything core locally first.
- Check CI after a merge. Releases are fixed forward, never by moving a tag.
