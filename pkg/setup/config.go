package setup

import (
	"time"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"

	"github.com/SkycoinProject/dmsg/cipher"
)

//go:generate readmegen -n Config -o ./README.md ./config.go

// Various timeouts for setup node.
const (
	RequestTimeout = time.Second * 60
	ReadTimeout    = time.Second * 30
)

// Config defines configuration parameters for setup Node.
type Config struct {
	PubKey cipher.PubKey `json:"public_key"`
	SecKey cipher.SecKey `json:"secret_key"`

	Dmsg snet.DmsgConfig `json:"dmsg"`

	TransportDiscovery string `json:"transport_discovery"`

	LogLevel string `json:"log_level"`
}
