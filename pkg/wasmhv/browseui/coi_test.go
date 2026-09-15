// Package browseui pkg/wasmhv/browseui/coi_test.go c3-vis-wasm
package browseui

import (
	"strings"
	"testing"
)

// TestCOIHalvesShipTogether. Isolation takes BOTH files, served from the same
// directory as the page: the worker synthesizes the headers, the register
// script installs it and reloads once. Either alone is inert — the worker with
// nothing to register it never installs, and the register script without the
// worker to fetch 404s and gives up. They are staged as a pair by
// scripts/stage-playground; this is the assertion that keeps them one.
func TestCOIHalvesShipTogether(t *testing.T) {
	sw, reg := string(COISWJS()), string(COIRegisterJS())
	if sw == "" || reg == "" {
		t.Fatalf("a half is missing: coi-sw.js=%d bytes, coi-register.js=%d bytes", len(sw), len(reg))
	}
	// The three headers ARE the capability. require-corp and same-origin
	// together are what the browser checks before granting isolation; drop one
	// and crossOriginIsolated stays false with nothing to show for the worker.
	for _, h := range []string{
		"Cross-Origin-Embedder-Policy",
		"Cross-Origin-Opener-Policy",
		"Cross-Origin-Resource-Policy",
	} {
		if !strings.Contains(sw, h) {
			t.Errorf("coi-sw.js does not set %s", h)
		}
	}
	// The reload is once per session or it is a reload loop on any page the
	// browser will never isolate.
	if !strings.Contains(reg, "coi-sw-reloaded") {
		t.Error("coi-register.js lost its session guard — an un-isolatable page would reload forever")
	}
	// It has to name the worker it registers, at the relative path the page
	// serves it from. An absolute '/coi-sw.js' would miss on a subpath host,
	// which is the host this exists for.
	if !strings.Contains(reg, "'coi-sw.js'") {
		t.Error("coi-register.js does not register coi-sw.js by relative name")
	}
}
