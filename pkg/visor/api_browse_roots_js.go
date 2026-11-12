//go:build js && wasm

// Package visor pkg/visor/api_browse_roots_js.go c3-vis-core
package visor

import (
	"crypto/x509"

	"github.com/skycoin/skywire/pkg/wasmhv/cabundle"
)

// browseRootCAs is the embedded Mozilla bundle: js/wasm has no system cert
// pool, so a browse fetch of an https:// page could verify nothing.
func browseRootCAs() *x509.CertPool { return cabundle.Pool() }
