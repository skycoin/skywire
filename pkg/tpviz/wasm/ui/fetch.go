//go:build js && wasm

package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

// DMSGData represents the /api/dmsg response
type DMSGData struct {
	Servers      []DMSGServer `json:"servers"`
	Entries      []string     `json:"entries"`
	EntriesCount int          `json:"entries_count"`
}

// DMSGServer represents a DMSG server
type DMSGServer struct {
	PK                string   `json:"pk"`
	Address           string   `json:"address"`
	Country           string   `json:"country"`
	AvailableSessions int      `json:"available_sessions"`
	Clients           []string `json:"clients"`
}

// IPGroupsData represents the /api/ip-groups response
type IPGroupsData struct {
	Enabled     bool           `json:"enabled"`
	TotalGroups int            `json:"total_groups"`
	Groups      map[string]int `json:"groups"` // pk -> group number
}

// AppState represents an app from /api/apps
type AppState struct {
	Name           string   `json:"name"`
	Status         int      `json:"status"` // 0=stopped, 1=running, 2=errored, 3=starting
	DetailedStatus string   `json:"detailed_status"`
	AutoStart      bool     `json:"auto_start"`
	Port           int      `json:"port"`
	Args           []string `json:"args,omitempty"`
}

// PingResponse represents the /api/ping response (matching TypeScript types.ts PingResponse)
type PingResponse struct {
	Status     string    `json:"status"`      // "success", "timeout", "error"
	Error      string    `json:"error,omitempty"`
	Mode       string    `json:"mode,omitempty"`
	Latencies  []float64 `json:"latencies,omitempty"`
	AvgMs      float64   `json:"avg_ms,omitempty"`
	MinMs      float64   `json:"min_ms,omitempty"`
	MaxMs      float64   `json:"max_ms,omitempty"`
	PacketLoss float64   `json:"packet_loss,omitempty"`
}

// AddTransportRequest represents a request to add a transport
type AddTransportRequest struct {
	RemotePK string `json:"remote_pk"`
	Type     string `json:"type"`
}

// AddTransportResponse represents the response from add transport
type AddTransportResponse struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	LocalPK  string `json:"local_pk"`
	RemotePK string `json:"remote_pk"`
	Error    string `json:"error,omitempty"`
}

// AppControlRequest represents a request to start/stop an app
type AppControlRequest struct {
	Name string `json:"name"`
}

// AppAutoStartRequest represents a request to set auto-start
type AppAutoStartRequest struct {
	Name      string `json:"name"`
	AutoStart bool   `json:"auto_start"`
}

// AppSetPKRequest represents a request to set server PK for an app
type AppSetPKRequest struct {
	Name string `json:"name"`
	PK   string `json:"pk"`
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

// FetchDMSG fetches DMSG server and client data
func (f *DataFetcher) FetchDMSG() (*DMSGData, error) {
	var data DMSGData
	err := fetchJSON("/api/dmsg/servers", &data)
	if err != nil {
		return nil, err
	}
	return &data, nil
}

// FetchIPGroups fetches IP group data for clustering
func (f *DataFetcher) FetchIPGroups() (*IPGroupsData, error) {
	var data IPGroupsData
	err := fetchJSON("/api/ip-groups", &data)
	if err != nil {
		return nil, err
	}
	return &data, nil
}

// FetchApps fetches running apps from the local visor
func (f *DataFetcher) FetchApps() ([]AppState, error) {
	var apps []AppState
	err := fetchJSON("/api/apps", &apps)
	return apps, err
}

// Ping performs a network ping to a remote visor (matching TypeScript ping.ts performPing)
func (f *DataFetcher) Ping(pk, mode string, tries int, localRoute bool) (*PingResponse, error) {
	url := "/api/ping?pk=" + pk + "&mode=" + mode + "&tries=" + itoa(tries)
	if mode == "route" && localRoute {
		url += "&local=true"
	}
	var resp PingResponse
	err := fetchJSON(url, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// postJSON posts JSON data to a URL and returns the response
func postJSON(url string, body interface{}, target interface{}) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return err
	}

	resp, err := http.Post(url, "application/json", strings.NewReader(string(jsonBody)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	return json.Unmarshal(respBody, target)
}

// LocalAddTransport creates a transport from the local visor (matching TypeScript tps.ts localCreateTransport)
func (f *DataFetcher) LocalAddTransport(remotePK, tpType string) (*AddTransportResponse, error) {
	var resp AddTransportResponse
	err := postJSON("/api/local/add-transport", AddTransportRequest{
		RemotePK: remotePK,
		Type:     tpType,
	}, &resp)
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return &resp, nil
}

// StartApp starts an application (matching TypeScript apps.ts startApp)
func (f *DataFetcher) StartApp(name string) error {
	var resp struct {
		Error string `json:"error,omitempty"`
	}
	err := postJSON("/api/apps/start", AppControlRequest{Name: name}, &resp)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// StopApp stops an application (matching TypeScript apps.ts stopApp)
func (f *DataFetcher) StopApp(name string) error {
	var resp struct {
		Error string `json:"error,omitempty"`
	}
	err := postJSON("/api/apps/stop", AppControlRequest{Name: name}, &resp)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// SetAppAutoStart sets the auto-start flag for an app (matching TypeScript apps.ts toggleAutoStart)
func (f *DataFetcher) SetAppAutoStart(name string, autoStart bool) error {
	var resp struct {
		Error string `json:"error,omitempty"`
	}
	err := postJSON("/api/apps/autostart", AppAutoStartRequest{Name: name, AutoStart: autoStart}, &resp)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// SetAppPK sets the server PK for an app (matching TypeScript apps.ts setAppPK)
func (f *DataFetcher) SetAppPK(name, pk string) error {
	var resp struct {
		Error string `json:"error,omitempty"`
	}
	err := postJSON("/api/apps/set-pk", AppSetPKRequest{Name: name, PK: pk}, &resp)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// ── TPS (Transport Setup) API methods (matching TypeScript tps.ts) ──

// TPSAddTransportRequest represents a request to add a transport via TPS
type TPSAddTransportRequest struct {
	TargetPK string `json:"target_pk"`
	RemotePK string `json:"remote_pk"`
	Type     string `json:"type"`
}

// TPSAddTransportResponse represents the response from TPS add transport
type TPSAddTransportResponse struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	LocalPK  string `json:"local_pk"`
	RemotePK string `json:"remote_pk"`
	Error    string `json:"error,omitempty"`
}

// TPSTransport represents a transport from the remote visor
type TPSTransport struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	LocalPK  string `json:"local_pk"`
	RemotePK string `json:"remote_pk"`
}

// TPSAddTransport adds a transport between two remote visors via TPS
func (f *DataFetcher) TPSAddTransport(targetPK, remotePK, tpType string) (*TPSAddTransportResponse, error) {
	var resp TPSAddTransportResponse
	err := postJSON("/api/tps/add-transport", TPSAddTransportRequest{
		TargetPK: targetPK,
		RemotePK: remotePK,
		Type:     tpType,
	}, &resp)
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return &resp, nil
}

// TPSRefreshTransports gets the current transports from a remote visor
func (f *DataFetcher) TPSRefreshTransports(pk string) ([]TPSTransport, error) {
	var transports []TPSTransport
	err := fetchJSON("/api/tps/refresh-transports?pk="+pk, &transports)
	return transports, err
}

// TPSRemoveTransportRequest represents a request to remove a transport via TPS
type TPSRemoveTransportRequest struct {
	TargetPK string `json:"target_pk"`
	ID       string `json:"id"`
}

// TPSRemoveTransport removes a transport from a remote visor via TPS
func (f *DataFetcher) TPSRemoveTransport(targetPK, tpID string) error {
	var resp struct {
		Error string `json:"error,omitempty"`
	}
	err := postJSON("/api/tps/remove-transport", TPSRemoveTransportRequest{
		TargetPK: targetPK,
		ID:       tpID,
	}, &resp)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// ── DMSG Health Check API (matching TypeScript dmsg.ts) ──

// DMSGHealthResponse represents the response from DMSG health check
type DMSGHealthResponse struct {
	Status    string `json:"status"`
	BuildInfo string `json:"build_info,omitempty"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
}

// DMSGHealthCheck checks DMSG connectivity to a remote visor
func (f *DataFetcher) DMSGHealthCheck(pk string) (*DMSGHealthResponse, error) {
	var resp DMSGHealthResponse
	err := fetchJSON("/api/dmsg/health?pk="+pk, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
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
