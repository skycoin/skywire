//go:build mobile

// Package tpviz pkg/tpviz/tpviz_legacy_off.go c4-app-rewards
package tpviz

import "embed"

// Mobile build: the legacy tpviz UI (~2.8 MB) is NOT embedded — the phone app
// never renders tpviz. legacyFS stays empty so every ReadFile on it errors and
// the tp-viz routes answer 404/500, mirroring the tpviz_wasm_off.go pattern.
var legacyFS embed.FS
