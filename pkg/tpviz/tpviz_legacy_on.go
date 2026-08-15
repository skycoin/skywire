//go:build !mobile

// Package tpviz pkg/tpviz/tpviz_legacy_on.go c4-app-rewards
package tpviz

import "embed"

// legacyFS holds the legacy JavaScript tpviz UI (~2.8 MB: index.html,
// bundle.js, textures). The `mobile` build variant leaves it empty
// (tpviz_legacy_off.go).
//
//go:embed legacy/*
var legacyFS embed.FS
