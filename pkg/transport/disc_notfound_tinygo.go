//go:build tinygo

package transport

// isDiscNotFound is the TinyGo stub. The native build matches a discovery 404 via
// httputil.HTTPError (net/http), which is excluded here; under TinyGo a status
// update never reaches an HTTP discovery, so there is no 404 to special-case.
func isDiscNotFound(err error) bool { return false }
