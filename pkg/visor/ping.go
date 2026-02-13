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
	Size     int  `json:"size"`
	EchoFull bool `json:"echo_full,omitempty"` // If true, server echoes full payload for bandwidth testing
}

// BandwidthTestConfig contains parameters for bandwidth testing
type BandwidthTestConfig struct {
	PK         cipher.PubKey
	Duration   time.Duration // How long to run the test
	PacketSize int           // Size of each packet in KB
	LocalRoute bool          // Use local route calculation
}

// BandwidthResult contains the results of a bandwidth test
type BandwidthResult struct {
	BytesSent     uint64        `json:"bytes_sent"`
	BytesReceived uint64        `json:"bytes_received"`
	Duration      time.Duration `json:"duration"`
	UploadSpeed   float64       `json:"upload_speed_kbps"`   // KB/s
	DownloadSpeed float64       `json:"download_speed_kbps"` // KB/s
}
