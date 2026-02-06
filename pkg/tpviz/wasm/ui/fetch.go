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

// DataFetcher fetches data from the API
type DataFetcher struct{}

// NewDataFetcher creates a new data fetcher
func NewDataFetcher() *DataFetcher {
	return &DataFetcher{}
}

// FetchTransports fetches transport data from the API
func (f *DataFetcher) FetchTransports() ([]TransportData, error) {
	resp, err := http.Get("/api/transports")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var transports []TransportData
	if err := json.Unmarshal(body, &transports); err != nil {
		return nil, err
	}

	return transports, nil
}

// FetchUptimes fetches uptime data from the API
func (f *DataFetcher) FetchUptimes() ([]UptimeEntry, error) {
	resp, err := http.Get("/api/uptimes")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var entries []UptimeEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, err
	}

	return entries, nil
}

// FetchServices fetches service discovery data from the API
func (f *DataFetcher) FetchServices() (map[string]ServiceInfo, error) {
	resp, err := http.Get("/api/services")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var services map[string]ServiceInfo
	if err := json.Unmarshal(body, &services); err != nil {
		return nil, err
	}

	return services, nil
}

// ProcessTransports converts transport data into graph nodes and edges
func ProcessTransports(transports []TransportData, uptimes []UptimeEntry, services map[string]ServiceInfo) *Graph {
	g := NewGraph()

	// Build uptime lookup
	uptimeLookup := make(map[string]*UptimeEntry)
	for i := range uptimes {
		uptimeLookup[uptimes[i].PK] = &uptimes[i]
	}

	// First pass: count connections for each node (matching JS)
	connectionCount := make(map[string]int)
	for _, tp := range transports {
		if len(tp.Edges) != 2 {
			continue
		}
		connectionCount[tp.Edges[0]]++
		connectionCount[tp.Edges[1]]++
	}

	// Find max connections for sizing
	maxConn := 1
	for _, count := range connectionCount {
		if count > maxConn {
			maxConn = count
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

				// Calculate node size like JS: 5 + (connections / maxConn) * 25
				connections := connectionCount[pk]
				size := 5.0 + (float64(connections)/float64(maxConn))*25.0

				node := &Node{
					ID:     pk,
					Size:   size,
					Status: StatusUnknown,
					Label:  shortPK(pk),
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
