//go:build !js

// Package visorconfig pkg/visor/visorconfig/hypervisorconfig_native.go
//
// Native (non-WASM) implementation of CookieConfig.SameSite —
// returns http.SameSite, so it can't compile under GOOS=js without
// dragging net/http into the WASM build graph. See services.go's
// package doc for the broader rationale.
package visorconfig

import (
	"net/http"
)

// SameSite gets cookie's `SameSite` value.
func (c *CookieConfig) SameSite() http.SameSite {
	// using default value for now
	return http.SameSiteDefaultMode
}
