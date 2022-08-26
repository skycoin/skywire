// Package setup node
package setup

import (
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsgc"
)

// Various timeouts for setup node.
const (
	RequestTimeout = time.Second * 60
	ReadTimeout    = time.Second * 30
)

// Config defines configuration parameters for setup Node.
type Config struct {
	PK                 cipher.PubKey    `json:"public_key"`
	SK                 cipher.SecKey    `json:"secret_key"`
	Dmsg               dmsgc.DmsgConfig `json:"dmsg"`
	TransportDiscovery string           `json:"transport_discovery"`
	LogLevel           string           `json:"log_level"`
}
