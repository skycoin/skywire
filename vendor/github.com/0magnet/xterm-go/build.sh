#!/bin/sh
# Build the demo into docs/, which is what GitHub Pages serves.
#
# TinyGo only: docs/ here carries one wasm and TinyGo's wasm_exec.js, and the
# demo is small enough that the stdlib build buys nothing worth ten times the
# download.
set -eu

cd "$(dirname "$0")"
mkdir -p docs
tinygo build -o docs/main.wasm -target wasm -no-debug ./examples/demo
cp "$(tinygo env TINYGOROOT)/targets/wasm_exec.js" docs/wasm_exec.js
ls -lh docs/main.wasm
