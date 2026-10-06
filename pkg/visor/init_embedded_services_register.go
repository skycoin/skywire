//go:build !mobile

// Package visor pkg/visor/init_embedded_services_register.go c3-vis-core
package visor

// The embeddable deployment services register their factories at init. A
// phone never runs them, so the mobile build leaves them, and the redis
// client they bring, out of the binary.
import (
	_ "github.com/skycoin/skywire/pkg/services/ar"  // address resolver
	_ "github.com/skycoin/skywire/pkg/services/rf"  // route finder
	_ "github.com/skycoin/skywire/pkg/services/sd"  // service discovery
	_ "github.com/skycoin/skywire/pkg/services/tpd" // transport discovery
)
