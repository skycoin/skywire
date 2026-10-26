package geoip

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/oschwald/geoip2-golang/v2"
)

// Handler answers a lookup of ?ip=, or of the caller's own address when it is
// not given, as JSON.
func Handler(db *geoip2.Reader) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queryIP := strings.TrimSpace(r.URL.Query().Get("ip"))
		if queryIP == "" {
			queryIP = ClientIP(r)
		}
		if queryIP == "" {
			http.Error(w, "could not determine client IP", http.StatusBadRequest)
			return
		}
		res, err := Lookup(db, queryIP)
		if err != nil {
			http.Error(w, fmt.Sprintf("lookup failed: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			http.Error(w, fmt.Sprintf("failed to encode response: %v", err), http.StatusInternalServerError)
		}
	})
}

// ClientIP is the caller's address: X-Real-Ip or the first X-Forwarded-For
// entry when a proxy set them, else the connection's remote address.
func ClientIP(r *http.Request) string {
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-Ip")); realIP != "" {
		return realIP
	}
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	if r.RemoteAddr == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
