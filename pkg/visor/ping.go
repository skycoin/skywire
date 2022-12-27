// Package visor pkg/visor/ping.go
package visor

import (
	"net"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

type ping struct {
	conn    net.Conn
	latency chan time.Duration
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
