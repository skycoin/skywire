//go:build mobile

// Package visor pkg/visor/static_mobile.go c3-vis-core
package visor

import "embed"

// ui is EMPTY on the mobile build: the ~6.8 MB manager UI never ships to the
// phone. It stays a valid embed.FS VALUE (not nil) on purpose — initUI()'s
// fs.Sub still yields a usable empty FS, so the `*uiAssets == nil` fatal in
// cmd.go never trips when a hypervisor config section is present. "/" is
// served by the mobile uiHandler stub (hypervisor_handlers_browse_mobile.go).
var ui embed.FS
