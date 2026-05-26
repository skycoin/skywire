//go:build !js

// Package visorconfig pkg/visor/visorconfig/values_native.go
//
// Native (non-WASM) implementations of helpers that need to shell
// out or touch the host filesystem in ways that don't make sense in
// a browser WASM context. Mirror counterpart values_js.go contains
// no-op stubs for the same surface.

package visorconfig

import (
	"os"
	"os/exec"
	"strings"

	"github.com/bitfield/script"
)

// resolveVersion implements the version-discovery fallback for
// Version(): when buildinfo.Version() returns "unknown" and we're
// running from a source checkout (.git present), shell out to
// `git describe --always`. Returns the input unchanged when not
// running from a source tree or when buildinfo already has a real
// version.
func resolveVersion(buildVersion string) string {
	if buildVersion != "unknown" {
		return buildVersion
	}
	// Check for .git folder for versioning.
	if _, err := os.Stat(".git"); err != nil {
		return buildVersion
	}
	if _, err := exec.LookPath("git"); err != nil {
		return buildVersion
	}
	v, err := script.Exec(`git describe --always`).String()
	if err != nil {
		return buildVersion
	}
	return strings.TrimSpace(v)
}
