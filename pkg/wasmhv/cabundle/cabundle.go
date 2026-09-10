// Package cabundle pkg/wasmhv/cabundle/cabundle.go c3-vis-wasm
//
// A Mozilla CA root bundle for the js/wasm builds: Go's js/wasm runtime has NO
// system cert pool (crypto/x509.SystemCertPool fails), so any TLS a wasm
// instance terminates itself — https through a SOCKS proxy on the page's
// virtual loopback, above all — would otherwise fail to verify. Verification
// is not optional there: the tab does TLS end-to-end to the origin precisely so
// the exit cannot read or MITM the stream, and skipping it hands the exit
// exactly that power.
//
// Used by the desk host (pkg/wasmhv/deskhost, the shell's `curl -x`).
package cabundle

import (
	"crypto/x509"
	_ "embed"
	"sync"
)

//go:embed cacert.pem
var pem []byte

// Pool parses the embedded bundle on FIRST USE, not at package init.
//
// The bundle is 186 KB / 122 certs, and parsing it measures ~47ms, 853 KB and
// ~10,000 allocations under js/wasm — paid on the one thread js/wasm has,
// before anything the instance does, if it ran at init. It is needed only on
// HTTPS-through-proxy paths a session may never reach, so it is behind a
// sync.Once, the same shape as pkg/geoip's lazily-inflated embed.
var Pool = sync.OnceValue(func() *x509.CertPool {
	p := x509.NewCertPool()
	p.AppendCertsFromPEM(pem)
	return p
})
