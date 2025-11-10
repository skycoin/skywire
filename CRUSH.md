# Skywire Development Guide

## Build Commands
- Build: `make build` or `GO111MODULE=on go build -ldflags="..." -mod=vendor -o ./build/skywire .`
- Build from source: `make build-merged`
- Clean: `make clean` (removes ./build and ./local)
- Install: `make install` (installs to $GOPATH/bin)

## Run Commands
- **Run visor from source** (no build required): `go run . cli config gen -n > config.json && go run . visor -c config.json`
- Apps run in-process as goroutines (no separate binaries needed)
- Config with empty `binary` field = in-process execution
- Config with `binary` field = external process execution

## Test Commands
- Run all tests: `make test` or `GO111MODULE=on go test -cover -timeout=5m -mod=vendor -tags no_ci ./internal/... ./pkg/... ./cmd/...`
- Run single test: `go test -v -run TestName ./path/to/package`
- Run package tests: `go test -v ./pkg/transport/...`
- Clean test cache: `go clean -testcache`

## Lint Commands
- Lint: `make lint` (runs golangci-lint on all packages)
- Format: `make format` (runs goimports and goimports-reviser)
- Install linters: `make install-linters`

## Code Style
- **Imports**: Group stdlib, external, then local (github.com/skycoin/skywire). Use goimports with `-local github.com/skycoin/skywire`
- **Package comments**: Start with `// Package name` on first line
- **Error variables**: Prefix with `Err`, define as package-level vars with descriptive comments
- **Constants**: Group related constants in const blocks with comments
- **Naming**: Use camelCase for unexported, PascalCase for exported. Acronyms are all caps (e.g., `dmsgC`, `arClient`)
- **Error handling**: Always check errors. Use `errors.New()` for static errors, `fmt.Errorf()` for dynamic
- **Logging**: Use `github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging` logger, not stdlib log
- **Testing**: Use testify/require for assertions. Test files use `package name_test` convention
- **Vendor mode**: Always use `-mod=vendor` flag for builds and tests
- **Line length**: Keep under 120 characters (per .golangci.yml)
- **Comments**: Document all exported functions, types, and constants

## Project Structure
- `cmd/` - Command-line tools and services
- `pkg/` - Public libraries and packages
- `internal/` - Private application code
- `static/` - UI assets (Angular app in skywire-manager-src/)
