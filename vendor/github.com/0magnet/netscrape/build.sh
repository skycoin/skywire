#!/bin/sh
# Build the standalone browser module that dist embeds. Run after changing the
# browser (browser.go or cmd/browser); the compressed result (dist/browser.wasm.gz)
# is committed so consumers of the dist subpackage get BrowserWasm() without a
# wasm build. Hosts that import netscrape.Open into their own wasm don't need this.
set -eu
cd "$(dirname "$0")"
GOOS=js GOARCH=wasm go build -o /tmp/netscrape-browser.wasm ./cmd/browser
gzip -9 -c /tmp/netscrape-browser.wasm > dist/browser.wasm.gz
echo "netscrape: dist/browser.wasm.gz updated ($(du -h dist/browser.wasm.gz | cut -f1))"
