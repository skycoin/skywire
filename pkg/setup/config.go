package setup

import (
	"time"

	"github.com/skycoin/dmsg/cipher"

	"github.com/skycoin/skywire/pkg/snet/dmsgc"
)

//go:generate readmegen -n Config -o ./README.md ./config.go

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
