//go:build !js

// Package visorconfig pkg/visor/visorconfig/hypervisorconfig_native.go
//
// Native (non-WASM) implementations for HypervisorConfig methods
// that touch disk or pull net/http / encoding/json. Kept off the
// js/wasm build graph so the install-page WASM (genvisor +
// autoconfigcmd) stays small and TinyGo-compatible.
//
//   - CookieConfig.SameSite returns http.SameSite (net/http).
//   - HypervisorConfig.Parse reads a config file from disk and
//     decodes via encoding/json — useless in a browser, harmful
//     for the WASM dep graph because encoding/json pulls reflect
//     runtime helpers that TinyGo's stdlib lacks.
package visorconfig

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

// SameSite gets cookie's `SameSite` value.
func (c *CookieConfig) SameSite() http.SameSite {
	// using default value for now
	return http.SameSiteDefaultMode
}

// Parse parses the file at path and decodes its JSON into c.
//
// Tagged !js because it pulls encoding/json (and via that, the
// reflect runtime helpers TinyGo's stdlib doesn't provide). Browsers
// don't have filesystem access anyway, so the method has no use
// under js/wasm — callers in that context should construct
// HypervisorConfig in memory.
func (c *HypervisorConfig) Parse(path string) error {
	var err error
	if path, err = filepath.Abs(path); err != nil {
		return err
	}

	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}

	defer func() {
		if err := f.Close(); err != nil {
			log.Fatalf("Failed to close file %s: %v", f.Name(), err)
		}
	}()

	return json.NewDecoder(f).Decode(c)
}
