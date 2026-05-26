//go:build js

// Package visorconfig pkg/visor/visorconfig/values_js.go
//
// js/wasm counterpart to the platform-gated values_{linux,darwin,
// windows}.go files. The browser doesn't have a host filesystem or
// hardware-inventory facility — none of UserConfig, SystemSurvey,
// or IsRoot make sense in a WASM context. The in-browser config
// generator never calls them; they exist here so the package's type
// surface (Survey) compiles cleanly under GOOS=js for callers that
// reference it indirectly via visorconfig.V1.

package visorconfig

import (
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// resolveVersion is the js counterpart of the native helper. No
// git fallback — returns the buildinfo version unchanged.
func resolveVersion(buildVersion string) string {
	return buildVersion
}

// UserConfig returns an empty PkgConfig. Browser WASM has no
// concept of a host install root.
func UserConfig() skyenv.PkgConfig {
	return skyenv.PkgConfig{}
}

// Survey system hardware survey struct. Browser-side fields are
// limited to what's meaningful without OS / hardware probes — the
// time of the survey and any caller-supplied identity fields. The
// Linux/Darwin/Windows variants populate sysinfo + ghw fields that
// don't exist here.
type Survey struct {
	Timestamp      time.Time     `json:"timestamp"`
	PubKey         cipher.PubKey `json:"public_key,omitempty"`
	SkycoinAddress string        `json:"skycoin_address,omitempty"`
	GOOS           string        `json:"go_os,omitempty"`
	GOARCH         string        `json:"go_arch,omitempty"`
	SkywireVersion string        `json:"skywire_version,omitempty"`
	ServicesURLs   Services      `json:"services,omitempty"`
	DmsgServers    []string      `json:"dmsg_servers,omitempty"`
}

// SystemSurvey returns an empty Survey on js — no hardware probes.
func SystemSurvey() (Survey, error) {
	return Survey{Timestamp: time.Now()}, nil
}

// IsRoot returns false on js. The browser doesn't have a uid
// concept that maps to root.
func IsRoot() bool {
	return false
}
