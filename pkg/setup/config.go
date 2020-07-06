package setup

import (
	"time"

	"github.com/SkycoinProject/dmsg/cipher"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
)

//go:generate readmegen -n Config -o ./README.md ./config.go

// Various timeouts for setup node.
const (
	RequestTimeout = time.Second * 60
	ReadTimeout    = time.Second * 30
)

// Config defines configuration parameters for setup Node.
type Config struct {
	PK                 cipher.PubKey   `json:"public_key"`
	SK                 cipher.SecKey   `json:"secret_key"`
	Dmsg               snet.DmsgConfig `json:"dmsg"`
	TransportDiscovery string          `json:"transport_discovery"`
	LogLevel           string          `json:"log_level"`
}
