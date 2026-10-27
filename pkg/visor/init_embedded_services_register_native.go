//go:build !mobile && !js

// Package visor pkg/visor/init_embedded_services_register_native.go c3-vis-core
package visor

// The services only a native deployment host runs. The skywire svc and dmsg
// commands register them too, but TinyGo builds leave svc out, so the visor
// registers them itself.
import (
	_ "github.com/skycoin/skywire/pkg/services/confbs"   // config bootstrapper
	_ "github.com/skycoin/skywire/pkg/services/dmsgdisc" // dmsg discovery
	_ "github.com/skycoin/skywire/pkg/services/sn"       // route setup node
	_ "github.com/skycoin/skywire/pkg/services/stun"     // STUN server
	_ "github.com/skycoin/skywire/pkg/services/tps"      // transport setup node
)
