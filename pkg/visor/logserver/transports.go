// Package logserver pkg/visor/logserver/transports.go c3-vis-core
package logserver

import (
	"compress/gzip"
	"context"
	"net/http"
	"strings"
)

// TransportListProvider supplies this visor's signed transport list as a
// JSON body and the version that names the set.
type TransportListProvider interface {
	TransportListBody() (body []byte, version string, err error)
}

// SetTransportListProvider installs the provider behind GET /transports.
func (api *API) SetTransportListProvider(p TransportListProvider) {
	api.transportListProvider = p
}

type overTransportKey struct{}

// WithOverTransport marks a request context as having arrived over a skywire
// transport rather than a dmsg server.
func WithOverTransport(ctx context.Context) context.Context {
	return context.WithValue(ctx, overTransportKey{}, true)
}

// OverTransport reports whether the request arrived over a skywire transport.
func OverTransport(ctx context.Context) bool {
	v, _ := ctx.Value(overTransportKey{}).(bool) //nolint:errcheck
	return v
}

// transportList serves the signed transport list to callers that reached this
// visor over a transport, which only a visor on the network has. A bare dmsg
// client is refused. If-None-Match with the current version gets 304.
func (api *API) transportList(w http.ResponseWriter, req *http.Request) {
	if !OverTransport(req.Context()) {
		writeJSON(w, http.StatusForbidden, jsonObj{"error": "the transport list is served only over a skywire transport"})
		return
	}
	if api.transportListProvider == nil {
		writeJSON(w, http.StatusServiceUnavailable, jsonObj{"error": "transport list not available"})
		return
	}
	body, version, err := api.transportListProvider.TransportListBody()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, jsonObj{"error": err.Error()})
		return
	}
	etag := `"` + version + `"`
	w.Header().Set("ETag", etag)
	if req.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if strings.Contains(req.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		_, _ = zw.Write(body) //nolint:errcheck
		_ = zw.Close()        //nolint:errcheck
		return
	}
	_, _ = w.Write(body) //nolint:errcheck
}
