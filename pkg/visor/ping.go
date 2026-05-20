// Package visor pkg/visor/ping.go
package visor

import (
	"fmt"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router"
)

// PingRouteRef identifies a specific route to a peer. Index 0 is
// the primary/legacy route used by single-route callers; aux routes
// (1..N-1) are added by callers that establish multiple parallel
// routes to the same target — currently only `cli visor ping mux-bw`,
// future mux-aware proxies will join.
//
// The route map is keyed by this struct rather than by bare PK so
// that N parallel DialPing calls to the same peer can coexist
// without overwriting each other's conn / route hops. Legacy
// single-route callers stay PK-only via PingRoutePrimary(pk) or by
// leaving PingConfig.RouteIndex at its zero value.
type PingRouteRef struct {
	PK    cipher.PubKey
	Index int
}

// PingRoutePrimary returns the PingRouteRef for the primary route
// to a peer. Call sites that don't care about multi-route semantics
// pass this — keeps the existing single-route fast path unambiguous.
func PingRoutePrimary(pk cipher.PubKey) PingRouteRef {
	return PingRouteRef{PK: pk, Index: 0}
}

// String renders as "<pk>#<index>" — useful for logs and the
// rpc-grpc dmsg-port handler's per-route debug output.
func (r PingRouteRef) String() string {
	return fmt.Sprintf("%s#%d", r.PK, r.Index)
}

type ping struct {
	conn     net.Conn
	hops     []cipher.PubKey       // route hops (intermediate visors + destination) - for backwards compat
	hopInfos []router.RouteHopInfo // detailed hop info with transport IDs and types
	serverPK cipher.PubKey         // for dmsg pings: the DMSG server used for this connection
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
