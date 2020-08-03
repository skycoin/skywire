package setup

import (
	"time"

	"github.com/skycoin/dmsg/cipher"
)

// Various timeouts for setup node.
const (
	RequestTimeout = time.Second * 60
	ReadTimeout    = time.Second * 30
)

// Config defines configuration parameters for setup Node.
type Config struct {
	PubKey cipher.PubKey `json:"public_key"`
	SecKey cipher.SecKey `json:"secret_key"`

	Dmsg struct {
		Discovery     string `json:"discovery"`
		SessionsCount int    `json:"sessions_count"`
	}

	TransportDiscovery string `json:"transport_discovery"`

	LogLevel string `json:"log_level"`
}
