//go:build !tinygo

package transport

import (
	"net/http"

	"github.com/skycoin/skywire/pkg/httputil"
)

// isDiscNotFound reports whether err is a transport-discovery "404 Not Found"
// response — meaning the entry is already gone, so a status update targeting it
// can be treated as a no-op success. Lives in a native-only file because
// httputil/net/http are excluded from the TinyGo graph.
func isDiscNotFound(err error) bool {
	httpErr, ok := err.(*httputil.HTTPError)
	return ok && httpErr.Status == http.StatusNotFound
}
