//go:build mobile

// Package visor pkg/visor/service_health_age_mobile.go c3-vis-core
package visor

import "time"

// serviceHealthMaxAge: the phone probes the services at most every 30 s,
// however often its Home asks (service_health_cache.go).
const serviceHealthMaxAge = 30 * time.Second
