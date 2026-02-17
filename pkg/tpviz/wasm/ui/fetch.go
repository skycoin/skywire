//go:build js && wasm

package ui

import (
	"encoding/json"
	"io"
	"net/http"
)

// TransportData represents a transport from the API
type TransportData struct {
	TID   string   `json:"t_id"`
	Edges []string `json:"edges"` // [from_pk, to_pk]
	Type  string   `json:"type"`
}

// UptimeEntry represents an uptime tracker entry
type UptimeEntry struct {
	PK      string `json:"pk"`
	Online  bool   `json:"on"`
	Version string `json:"version,omitempty"`
}

// ServiceInfo represents service discovery info
type ServiceInfo struct {
	Services []string `json:"services"`
	Country  string   `json:"country"`
}

// LocalVisorData represents the local visor state from /api/local-visor
type LocalVisorData struct {
	Connected  bool             `json:"connected"`
	PubKey     string           `json:"pub_key"`
	Transports []LocalTransport `json:"transports"`
	Routes     []LocalRoute     `json:"routes"`
}

// LocalTransport represents a local visor transport
type LocalTransport struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	RemotePK  string `json:"remote_pk"`
	SentBytes int64  `json:"sent_bytes"`
	RecvBytes int64  `json:"recv_bytes"`
}

// LocalRoute represents a local visor route
type LocalRoute struct {
	RouteID     int    `json:"route_id"`
	Type        string `json:"type"`
	DstPK       string `json:"dst_pk"`
	NextHopPK   string `json:"next_hop_pk"`
	TransportID string `json:"transport_id"`
}

// HealthInfo represents the /api/health response
type HealthInfo struct {
	AutoRefresh   bool `json:"auto_refresh"`
	NextRefreshIn int  `json:"next_refresh_in"` // seconds
	CacheMaxAge   int  `json:"cache_max_age"`   // minutes
}

// TPSStatus represents the /api/tps/status response
type TPSStatus struct {
	Running bool   `json:"running"`
	TPSPK   string `json:"tps_pk,omitempty"`
}

// DataFetcher fetches data from the API
type DataFetcher struct{}

// NewDataFetcher creates a new data fetcher
func NewDataFetcher() *DataFetcher {
	return &DataFetcher{}
}

func fetchJSON(url string, target interface{}) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	return json.Unmarshal(body, target)
}

// FetchTransports fetches transport data from the API
func (f *DataFetcher) FetchTransports() ([]TransportData, error) {
	var transports []TransportData
	err := fetchJSON("/api/transports", &transports)
	return transports, err
}

// FetchUptimes fetches uptime data from the API
func (f *DataFetcher) FetchUptimes() ([]UptimeEntry, error) {
	var entries []UptimeEntry
	err := fetchJSON("/api/uptimes", &entries)
	return entries, err
}

// FetchServices fetches service discovery data from the API
func (f *DataFetcher) FetchServices() (map[string]ServiceInfo, error) {
	var services map[string]ServiceInfo
	err := fetchJSON("/api/services", &services)
	return services, err
}

// FetchLocalVisor fetches local visor data from the API
func (f *DataFetcher) FetchLocalVisor() (*LocalVisorData, error) {
	var data LocalVisorData
	err := fetchJSON("/api/local-visor", &data)
	if err != nil {
		return nil, err
	}
	return &data, nil
}

// FetchHealth fetches server health/cache info
func (f *DataFetcher) FetchHealth() (*HealthInfo, error) {
	var info HealthInfo
	err := fetchJSON("/api/health", &info)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// FetchTPSStatus fetches transport setup node status
func (f *DataFetcher) FetchTPSStatus() (*TPSStatus, error) {
	var status TPSStatus
	err := fetchJSON("/api/tps/status", &status)
	if err != nil {
		return nil, err
	}
	return &status, nil
}

// ProcessTransports converts transport data into graph nodes and edges
func ProcessTransports(transports []TransportData, uptimes []UptimeEntry, services map[string]ServiceInfo) *Graph {
	g := NewGraph()

	// Build uptime lookup
	uptimeLookup := make(map[string]*UptimeEntry)
	for i := range uptimes {
		uptimeLookup[uptimes[i].PK] = &uptimes[i]
	}

	// First pass: count connections per type for each node
	type connCounts struct {
		total, stcpr, sudph, dmsg int
	}
	counts := make(map[string]*connCounts)
	for _, tp := range transports {
		if len(tp.Edges) != 2 {
			continue
		}
		for _, pk := range tp.Edges {
			c, ok := counts[pk]
			if !ok {
				c = &connCounts{}
				counts[pk] = c
			}
			c.total++
			switch TransportType(tp.Type) {
			case TransportSTCPR:
				c.stcpr++
			case TransportSUDPH:
				c.sudph++
			case TransportDMSG:
				c.dmsg++
			}
		}
	}

	// Find max connections for sizing
	maxConn := 1
	for _, c := range counts {
		if c.total > maxConn {
			maxConn = c.total
		}
	}

	// Track nodes we've seen
	seenNodes := make(map[string]bool)

	// Process transports
	for _, tp := range transports {
		if len(tp.Edges) != 2 {
			continue
		}

		fromPK := tp.Edges[0]
		toPK := tp.Edges[1]

		// Create nodes if we haven't seen them
		for _, pk := range []string{fromPK, toPK} {
			if !seenNodes[pk] {
				seenNodes[pk] = true

				c := counts[pk]
				size := 5.0 + (float64(c.total)/float64(maxConn))*25.0

				node := &Node{
					ID:              pk,
					Size:            size,
					Status:          StatusUnknown,
					Label:           shortPK(pk),
					ConnectionCount: c.total,
					STCPRCount:      c.stcpr,
					SUDPHCount:      c.sudph,
					DMSGCount:       c.dmsg,
				}

				// Apply uptime data
				if ut, ok := uptimeLookup[pk]; ok {
					if ut.Online {
						node.Status = StatusOnline
					} else {
						node.Status = StatusOffline
					}
					node.Version = ut.Version
				}

				// Apply service data
				if svc, ok := services[pk]; ok {
					node.Country = svc.Country
					node.HasServices = len(svc.Services) > 0
					for _, s := range svc.Services {
						if s == "vpn" {
							node.Service = ServiceVPN
							break
						} else if s == "proxy" {
							node.Service = ServiceProxy
						}
					}
				}

				g.AddNode(node)
			}
		}

		// Create edge
		edgeID := tp.TID
		if edgeID == "" {
			edgeID = fromPK + "-" + toPK
		}

		edge := &Edge{
			ID:   edgeID,
			From: fromPK,
			To:   toPK,
			Type: TransportType(tp.Type),
		}

		g.AddEdge(edge)
	}

	return g
}
