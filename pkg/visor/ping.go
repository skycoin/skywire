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

// PingMsg ...
type PingMsg struct {
	Timestamp time.Time
	PingPk    cipher.PubKey
	Data      []byte
}
