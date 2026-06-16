# Makefile for the `skywire web` WASM client.
#
# Targets:
#   make build  = build static/b.wasm via TinyGo
#   make clean  = remove static/b.wasm
#
# The Go server (web.go) embeds static/b.wasm via //go:embed. An
# empty b.wasm is acceptable — the page renders a "b.wasm not built"
# banner — but a production binary should ship a real bundle, so
# the top-level skywire Makefile should chain into this target as
# part of a release-binary build (TODO: wire into Makefile:build).

WASM_OUT := static/b.wasm

.PHONY: build clean

build: $(WASM_OUT)

$(WASM_OUT): wasm/main.go
	tinygo build -target wasm -no-debug -o $@ ./wasm
	@echo "built $@ ($(shell stat -c %s $@) bytes)"

clean:
	rm -f $(WASM_OUT)
