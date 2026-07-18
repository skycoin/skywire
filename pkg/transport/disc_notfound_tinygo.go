//go:build tinygo

// Package transport pkg/transport/disc_notfound_tinygo.go c2-net-transport
package transport

// isDiscNotFound is the TinyGo stub. The native build matches a discovery 404 via
// httputil.HTTPError (net/http), which is excluded here; under TinyGo a status
// update never reaches an HTTP discovery, so there is no 404 to special-case.
func isDiscNotFound(err error) bool { return false }
