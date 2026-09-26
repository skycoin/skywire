//go:build !(js && wasm)

package visor

import "crypto/x509"

// browseRootCAs is nil on native builds: the system pool verifies.
func browseRootCAs() *x509.CertPool { return nil }
