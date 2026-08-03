//go:build mobile

// Package geoip pkg/geoip/geoip_embedded_mobile.go c0-com-util
package geoip

import (
	"errors"

	"github.com/oschwald/geoip2-golang/v2"
)

// The mobile build strips the ~30 MB embedded GeoLite2-City database — the
// single largest asset in the visor. Both visor consumers already degrade
// gracefully on the error path: LookupGeo → nil (pkg/visor/init_services.go)
// and routing-policy geo.country() → "??" (pkg/visor/policy_provider.go).

// EmbeddedDB returns nil: no database is embedded in the mobile build.
func EmbeddedDB() []byte { return nil }

// OpenEmbedded reports the database absent in the mobile build.
func OpenEmbedded() (*geoip2.Reader, error) {
	return nil, errors.New("geoip: embedded database not included in this build")
}
