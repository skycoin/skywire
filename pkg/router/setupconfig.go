// Package router pkg/router/
package router

import (
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsgc"
)

// Various timeouts for setup node.
const (
	RequestTimeout = time.Second * 60
	ReadTimeout    = time.Second * 30
)

// SetupConfig defines configuration parameters for setup Node.
type SetupConfig struct {
	PK                 cipher.PubKey    `json:"public_key"`
	SK                 cipher.SecKey    `json:"secret_key"`
	Dmsg               dmsgc.DmsgConfig `json:"dmsg"`
	TransportDiscovery string           `json:"transport_discovery"`
	LogLevel           string           `json:"log_level"`
}
