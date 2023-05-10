// Package ping pkg/visor/ping/ping.go
package ping

import (
	"net"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

// Conn contains the skywire conn and the latency of a route that is pinged
type Conn struct {
	Conn    net.Conn
	Latency chan time.Duration
}

// Msg is used to calculate the ping to a remote visor
type Msg struct {
	Timestamp time.Time
	PingPk    cipher.PubKey
	Data      []byte
}

// SizeMsg contains the size of the Msg to be sent
type SizeMsg struct {
	Size int
}
