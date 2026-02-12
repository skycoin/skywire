// Package visor pkg/visor/ping.go
package visor

import (
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

type ping struct {
	conn     net.Conn
	hops     []cipher.PubKey       // route hops (intermediate visors + destination) - for backwards compat
	hopInfos []router.RouteHopInfo // detailed hop info with transport IDs and types
}

// PingMsg is used to calculate the ping to a remote visor
type PingMsg struct {
	Timestamp time.Time
	PingPk    cipher.PubKey
	Data      []byte
}

// PingSizeMsg contains the size of the PingMsg to be sent
type PingSizeMsg struct {
	Size int
}
