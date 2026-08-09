//go:build !mobile

// Package visor pkg/visor/static_embed.go c3-vis-core
package visor

import "embed"

// ui holds the built Angular manager UI (static/, ~6.8 MB), served by
// uiHandler at "/" and exported via HypervisorUIFS (visor.go). The `mobile`
// build variant replaces it with an empty FS (static_mobile.go) — the phone
// core is API-only.
//
//go:embed static
var ui embed.FS
