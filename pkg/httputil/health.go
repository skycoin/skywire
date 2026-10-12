// Package httputil pkg/httputil/health.go c0-com-http
package httputil

import (
	"context"
	"net/http"
	"time"

	"github.com/skycoin/skywire/pkg/buildinfo"
)

var path = "/health"

// HealthCheckResponse is struct of /health endpoint
type HealthCheckResponse struct {
	ServiceName       string          `json:"service_name,omitempty"`
	BuildInfo         *buildinfo.Info `json:"build_info,omitempty"`
	StartedAt         time.Time       `json:"started_at"`
	PublicKey         string          `json:"public_key,omitempty"`
	DmsgAddr          string          `json:"dmsg_address,omitempty"`
	DmsgDiscovery     string          `json:"dmsg_discovery,omitempty"`
	DmsgServers       []string        `json:"dmsg_servers,omitempty"`
	PeerServers       []string        `json:"peer_servers,omitempty"`
	PublicAutoconnect bool            `json:"public_autoconnect,omitempty"`
	StcprCount        int             `json:"stcpr_count,omitempty"`
	SudphCount        int             `json:"sudph_count,omitempty"`
	// TransportCounts is a count of live transports keyed by their actual
	// network type (stcpr, sudph, stcp, dmsg, squicr, swsr, swtr, webrtc, …).
	// The key set is derived from the transports present, so every current and
	// future transport type is represented — not just stcpr/sudph. The legacy
	// StcprCount/SudphCount fields remain for backward compatibility.
	TransportCounts map[string]int `json:"transport_counts,omitempty"`
	NetworkTypes    []string       `json:"network_types,omitempty"`
	DHTBootstrap    bool           `json:"dht_bootstrap,omitempty"`
	// UDPAddr is an address resolver's public SUDPH address: visors that
	// reach it over dmsg register SUDPH with it. Empty when not configured.
	UDPAddr string `json:"udp_address,omitempty"`
	// DmsgSessions are the visor's own sessions with dmsg servers.
	DmsgSessions int `json:"dmsg_sessions,omitempty"`
	// DmsgServer is what the dmsg server this process runs is carrying.
	DmsgServer *DmsgServerHealth `json:"dmsg_server,omitempty"`
}

// DmsgServerHealth is a dmsg server's load: its connections, and the
// streams it relays and the bytes they carried since it started.
type DmsgServerHealth struct {
	ClientSessions int    `json:"client_sessions"`
	PeerSessions   int    `json:"peer_sessions"`
	ActiveStreams  int64  `json:"active_streams"`
	StreamsRelayed uint64 `json:"streams_relayed"`
	BytesUp        uint64 `json:"bytes_up"`
	BytesDown      uint64 `json:"bytes_down"`
}

// DmsgServerHealthOf converts a server's Stats for /health.
func DmsgServerHealthOf(clients, peers int, active int64, streams, up, down uint64) *DmsgServerHealth {
	return &DmsgServerHealth{ClientSessions: clients, PeerSessions: peers, ActiveStreams: active, StreamsRelayed: streams, BytesUp: up, BytesDown: down}
}

// GetServiceHealth gets the response from the given service url
func GetServiceHealth(ctx context.Context, url string) (health *HealthCheckResponse, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp != nil {
		defer func() {
			if cErr := resp.Body.Close(); cErr != nil && err == nil {
				err = cErr
			}
		}()
	}
	if resp.StatusCode != http.StatusOK {
		var hErr HTTPError
		if err = json.NewDecoder(resp.Body).Decode(&hErr); err != nil {
			return nil, err
		}
		return nil, &hErr
	}
	err = json.NewDecoder(resp.Body).Decode(&health)

	return health, nil
}
