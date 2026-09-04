#!/bin/sh
# Build the web view into docs/, which is what GitHub Pages serves.
#
# Both toolchains are carried: TinyGo at docs/ because it is a quarter the size
# and this is fetched over the network before anything appears, and the standard
# Go build at docs/go/ because TinyGo occasionally miscompiles something and
# having the other one a click away is how you find out that is what happened.
# The two pages link to each other.
#
#   ./build.sh          both
#   ./build.sh tinygo   TinyGo only
#   ./build.sh go       standard Go only
set -eu

cd "$(dirname "$0")"

build_tinygo() {
	# TinyGo trails each new Go release by some weeks; until it catches up, a
	# build against the newer Go fails outright. The helper reports the newest
	# Go this TinyGo accepts, or "auto" once the system one will do.
	GOTOOLCHAIN=$(sh scripts/tinygo-toolchain.sh); export GOTOOLCHAIN
	mkdir -p docs
	# The demo composes the desk with its panes, so it is built from the
	# panes module; the desk itself does not know they exist.
	(cd panes && tinygo build -o ../docs/desk.wasm -target wasm -no-debug ./cmd/desk)
	cp "$(tinygo env TINYGOROOT)/targets/wasm_exec.js" docs/wasm_exec.js
}

build_go() {
	mkdir -p docs/go
	(cd panes && GOOS=js GOARCH=wasm go build -o ../docs/go/desk.wasm ./cmd/desk)
	cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" docs/go/wasm_exec.js
}

case "${1:-both}" in
both)   build_tinygo; build_go ;;
tinygo) build_tinygo ;;
go)     build_go ;;
*)      echo "usage: $0 [both|tinygo|go]" >&2; exit 2 ;;
esac

ls -lh docs/desk.wasm docs/go/desk.wasm 2>/dev/null || true
