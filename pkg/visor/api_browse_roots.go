//go:build !(js && wasm)

// Package visor pkg/visor/api_browse_roots.go c3-vis-core
package visor

import "crypto/x509"

// browseRootCAs is nil on native builds: the system pool verifies.
func browseRootCAs() *x509.CertPool { return nil }
