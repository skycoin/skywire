//go:build js

// Package skyenv pkg/skyenv/skyenv_js.go
//
// js/wasm build of pkg/skyenv. Path constants are stubbed to
// empty/sentinel values — the browser doesn't have filesystem
// access in the host sense, and the in-browser install-page
// generator (apt-repo/cmd/wasm) only needs the *type* surface of
// skyenv to compile; it never reads SkywirePath or ConfigJSON at
// runtime.
package skyenv

// SkywirePath is the platform-specific install root. js/wasm:
// empty string. The install-page WASM doesn't perform host-side
// filesystem operations.
const SkywirePath = ""

// ConfigJSON is the platform-specific path component for the
// visor config file. js/wasm: empty string. The install-page WASM
// emits the config as a downloadable blob rather than writing it
// to a host path.
const ConfigJSON = ""
