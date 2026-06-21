package wasmhv

import _ "embed"

// OverrideJS is pkg/wasmhv/override.js — the classic <script> that, in a
// generated standalone file, boots the WASM dmsg client and routes the UI's
// /api over dmsg (or to the in-wasm core in standalone mode). Embedded so
// GenerateStandalone consumers don't need to locate it on disk.
//
//go:embed override.js
var OverrideJS []byte
