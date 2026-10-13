//go:build mobile

package visor

import "time"

// serviceHealthMaxAge: the phone probes the services at most every 30 s,
// however often its Home asks (service_health_cache.go).
const serviceHealthMaxAge = 30 * time.Second
