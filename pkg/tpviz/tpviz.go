// Package tpviz provides a web-based transport discovery visualizer
package tpviz

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

//go:embed legacy/*
var legacyFS embed.FS

//go:embed dist/*
var wasmDistFS embed.FS

// Config holds the configuration for the visualizer server
type Config struct {
	// Addr is the address to bind to (default: 127.0.0.1)
	Addr string
	// Port is the port to listen on (default: 8080)
	Port int
	// CacheFile is the location for the TPD cache file
	CacheFile string
	// CacheFileUT is the location for the uptime tracker cache file
	CacheFileUT string
	// CacheFileSD is the location for the service discovery cache file
	CacheFileSD string
	// CacheFileDMSGServers is the location for the DMSG servers cache file
	CacheFileDMSGServers string
	// CacheFileDMSGEntries is the location for the DMSG entries cache file
	CacheFileDMSGEntries string
	// CacheFileDMSGClients is the location for the DMSG server/clients cache file
	CacheFileDMSGClients string
	// CacheMaxAge is the cache max age in minutes (default: 5)
	CacheMaxAge int
	// TPDURL is the transport discovery URL
	TPDURL string
	// UTURL is the uptime tracker URL
	UTURL string
	// SDURL is the service discovery URL
	SDURL string
	// DMSGURL is the DMSG discovery URL
	DMSGURL string
	// NoCache disables caching when true
	NoCache bool
	// AutoRefresh enables auto-refresh of cache at specified interval
	AutoRefresh bool
	// SurveyDir is the directory containing visor surveys (node-info.json files)
	// Used for IP-based grouping without exposing actual IP addresses
	SurveyDir string
	// GeoIPURL is the URL of the geoip service (default: http://ip.skycoin.com)
	GeoIPURL string
}

// DefaultConfig returns a Config with default values
func DefaultConfig() Config {
	return Config{
		Addr:                 "127.0.0.1",
		Port:                 8080,
		CacheFile:            filepath.Join(os.TempDir(), "tpd.json"),
		CacheFileUT:          filepath.Join(os.TempDir(), "ut.json"),
		CacheFileSD:          filepath.Join(os.TempDir(), "sd.json"),
		CacheFileDMSGServers: filepath.Join(os.TempDir(), "dmsg-servers.json"),
		CacheFileDMSGEntries: filepath.Join(os.TempDir(), "dmsg-entries.json"),
		CacheFileDMSGClients: filepath.Join(os.TempDir(), "dmsg-clients.json"),
		CacheMaxAge:          5,
		TPDURL:               deployment.Prod.TransportDiscovery,
		UTURL:                deployment.Prod.UptimeTracker,
		SDURL:                deployment.Prod.ServiceDiscovery,
		DMSGURL:              deployment.Prod.DmsgDiscovery,
		NoCache:              false,
		AutoRefresh:          true,
		GeoIPURL:             "http://ip.skycoin.com",
	}
}

// VisorAPI defines the interface for accessing visor data.
// This is defined locally to avoid import cycles with the visor package.
type VisorAPI interface {
	Overview() (*VisorOverview, error)
	RoutingRules() ([]routing.Rule, error)
	AddTransport(ctx context.Context, remotePK, tpType string) (*TransportSummary, error)
	RemoveTransport(ctx context.Context, tpID string) error
	DMSGHealth(ctx context.Context, pk string) (*DMSGHealthResponse, error)
	Ping(ctx context.Context, pk string, useDMSG, localRoute bool, tries, sizeKB int) (*PingResponse, error)
	// App management
	Apps() ([]*AppState, error)
	StartApp(appName string) error
	StopApp(appName string) error
	SetAutoStart(appName string, autoStart bool) error
	SetAppPK(appName, pk string) error
	Close() error
}

// DMSGHealthResponse contains the response from a DMSG health check.
type DMSGHealthResponse struct {
	Status    string `json:"status"`
	BuildInfo string `json:"build_info,omitempty"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
}

// PingResponse contains the response from a ping operation.
type PingResponse struct {
	Status     string  `json:"status"`
	LatencyMs  float64 `json:"latency_ms,omitempty"`
	Latencies  []int64 `json:"latencies,omitempty"` // Individual latencies in ms
	MinMs      float64 `json:"min_ms,omitempty"`
	MaxMs      float64 `json:"max_ms,omitempty"`
	AvgMs      float64 `json:"avg_ms,omitempty"`
	PacketLoss float64 `json:"packet_loss,omitempty"`
	Error      string  `json:"error,omitempty"`
	Mode       string  `json:"mode"` // "route" or "dmsg"
}

// AppState represents the state of an application running on the visor.
type AppState struct {
	Name           string `json:"name"`
	Status         int    `json:"status"`          // 0=stopped, 1=running, 2=errored, 3=starting
	DetailedStatus string `json:"detailed_status"` // Human-readable status
	AutoStart      bool   `json:"auto_start"`
	Port           uint16 `json:"port"`
	Args           []string `json:"args,omitempty"`
}

// VisorOverview contains visor overview data.
// This mirrors visor.Overview but is defined locally to avoid import cycles.
type VisorOverview struct {
	PubKey      cipher.PubKey       `json:"local_pk"`
	Transports  []*TransportSummary `json:"transports"`
	RoutesCount int                 `json:"routes_count"`
}

// TransportSummary contains transport summary data.
// This mirrors visor.TransportSummary but is defined locally.
type TransportSummary struct {
	ID      uuid.UUID           `json:"id"`
	Local   cipher.PubKey       `json:"local_pk"`
	Remote  cipher.PubKey       `json:"remote_pk"`
	Type    tptypes.Type        `json:"type"`
	Log     *transport.LogEntry `json:"log,omitempty"`
	IsSetup bool                `json:"is_setup"`
	Label   transport.Label     `json:"label"`
}

// TPSAPI defines the interface for the embedded Transport Setup Node.
// Used to create/remove transports on remote visors and query their transport lists.
type TPSAPI interface {
	AddTransport(ctx context.Context, targetPK, remotePK, tpType string) (*TPSTransportResponse, error)
	RemoveTransport(ctx context.Context, targetPK, tpID string) error
	GetTransports(ctx context.Context, targetPK string) ([]TPSTransportResponse, error)
	PK() string
}

// TPSTransportResponse represents a transport returned from TPS RPC operations.
type TPSTransportResponse struct {
	ID     string `json:"id"`
	Local  string `json:"local_pk"`
	Remote string `json:"remote_pk"`
	Type   string `json:"type"`
}

// Server is the transport visualizer HTTP server
type Server struct {
	config   Config
	log      *logging.Logger
	mux      *http.ServeMux
	cacheMu  sync.RWMutex
	stopChan chan struct{}
	autoTick *time.Ticker

	// Cached IP groups data (refreshed periodically)
	ipGroupsMu    sync.RWMutex
	ipGroupsCache *ipGroupsResponse

	// Local visor RPC connection (optional)
	visorMu       sync.RWMutex
	visorAPI      VisorAPI
	visorConn     net.Conn
	visorCache    *LocalVisorData
	embeddedMode  bool                         // true when visor API is set directly (not via RPC)
	prevBandwidth map[string]bandwidthSnapshot // track previous bandwidth for deltas

	// Websocket clients for local visor data streaming
	wsClientsMu sync.RWMutex
	wsClients   map[*websocket.Conn]struct{}
	wsBroadcast chan []byte

	// Local visor geoip data (fetched once at startup)
	localGeoMu sync.RWMutex
	localGeo   *localGeoData

	// Embedded TPS API (optional — nil when tps_sk not configured)
	tpsMu  sync.RWMutex
	tpsAPI TPSAPI

	// DMSG discovery cache
	dmsgMu    sync.RWMutex
	dmsgCache *DMSGData
}

// localGeoData holds geoip information for the local visor
type localGeoData struct {
	IP      string `json:"ip_address"`
	Country string `json:"country_code"`
}

// DMSGData holds cached DMSG discovery data
type DMSGData struct {
	Servers      []DMSGServer `json:"servers"`
	EntriesCount int          `json:"entries_count"`
	Entries      []string     `json:"entries"` // list of all entry PKs
	LastUpdated  time.Time    `json:"last_updated"`
}

// DMSGServer represents a DMSG server entry
type DMSGServer struct {
	PK                string   `json:"pk"`
	Address           string   `json:"address"`
	AvailableSessions int      `json:"available_sessions"`
	ServerType        string   `json:"server_type"`
	IP                string   `json:"ip,omitempty"`
	Country           string   `json:"country,omitempty"`
	Clients           []string `json:"clients,omitempty"` // PKs of clients connected to this server
}

// fetchLocalGeoIP fetches the local visor's IP and country from the geoip service
func (s *Server) fetchLocalGeoIP() {
	if s.config.GeoIPURL == "" {
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(s.config.GeoIPURL)
	if err != nil {
		s.log.WithError(err).Warn("Failed to fetch geoip data")
		return
	}
	defer resp.Body.Close() //nolint:errcheck

	var geo localGeoData
	if err := json.NewDecoder(resp.Body).Decode(&geo); err != nil {
		s.log.WithError(err).Warn("Failed to parse geoip response")
		return
	}

	s.localGeoMu.Lock()
	s.localGeo = &geo
	s.localGeoMu.Unlock()

	if geo.Country != "" {
		s.log.WithField("ip", geo.IP).WithField("country", geo.Country).Info("Local visor geoip")
	}
}

// LocalVisorData holds data from the local visor for overlay display
type LocalVisorData struct {
	Connected   bool             `json:"connected"`
	PubKey      string           `json:"pub_key,omitempty"`
	Country     string           `json:"country,omitempty"`
	IP          string           `json:"ip,omitempty"`
	Transports  []LocalTransport `json:"transports,omitempty"`
	Routes      []LocalRoute     `json:"routes,omitempty"`
	RoutesCount int              `json:"routes_count"`
	LastUpdated time.Time        `json:"last_updated"`
	// Bandwidth deltas for animation (bytes since last update)
	TotalSentDelta uint64 `json:"total_sent_delta"`
	TotalRecvDelta uint64 `json:"total_recv_delta"`
}

// LocalTransport represents a transport from the local visor with traffic stats
type LocalTransport struct {
	ID        string `json:"id"`
	RemotePK  string `json:"remote_pk"`
	Type      string `json:"type"`
	Label     string `json:"label,omitempty"`
	IsSetup   bool   `json:"is_setup"`
	SentBytes uint64 `json:"sent_bytes"`
	RecvBytes uint64 `json:"recv_bytes"`
	// Bandwidth deltas for animation
	SentDelta uint64 `json:"sent_delta"`
	RecvDelta uint64 `json:"recv_delta"`
}

// LocalRoute represents a routing rule from the local visor
type LocalRoute struct {
	RouteID     uint32 `json:"route_id"`
	Type        string `json:"type"` // "forward", "consume", "intermediary"
	SrcPK       string `json:"src_pk,omitempty"`
	DstPK       string `json:"dst_pk,omitempty"`
	NextHopPK   string `json:"next_hop_pk,omitempty"` // For forward/intermediary routes
	TransportID string `json:"transport_id,omitempty"`
}

// bandwidthSnapshot tracks bandwidth for delta calculation
type bandwidthSnapshot struct {
	sent uint64
	recv uint64
}

// ipGroupsResponse is the cached response for /api/ip-groups
type ipGroupsResponse struct {
	Enabled     bool           `json:"enabled"`
	Groups      map[string]int `json:"groups"`
	TotalGroups int            `json:"total_groups"`
}

// NewServer creates a new visualizer server with the given config
func NewServer(cfg Config) *Server {
	s := &Server{
		config:        cfg,
		log:           logging.MustGetLogger("tp_viz"),
		mux:           http.NewServeMux(),
		stopChan:      make(chan struct{}),
		prevBandwidth: make(map[string]bandwidthSnapshot),
		wsClients:     make(map[*websocket.Conn]struct{}),
		wsBroadcast:   make(chan []byte, 100),
	}
	s.setupRoutes()
	return s
}

// SetVisorAPI sets the visor API directly, bypassing RPC connection.
// This is used when the tp-viz server is embedded in the visor itself.
func (s *Server) SetVisorAPI(api VisorAPI, pubKey string) {
	s.visorMu.Lock()
	defer s.visorMu.Unlock()

	// Close existing connection if any
	if s.visorAPI != nil {
		s.visorAPI.Close() //nolint:errcheck,gosec
	}

	s.visorAPI = api
	s.embeddedMode = true // Mark as embedded mode - API is always valid
	s.visorCache = &LocalVisorData{
		Connected: true,
		PubKey:    pubKey,
	}
}

// SetTPSAPI sets the embedded Transport Setup Node API.
// When set, the UI will show transport creation controls.
func (s *Server) SetTPSAPI(api TPSAPI) {
	s.tpsMu.Lock()
	defer s.tpsMu.Unlock()
	s.tpsAPI = api
}

func (s *Server) setupRoutes() {
	// Serve legacy JavaScript UI at / (primary)
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" || path == "/index.html" {
			content, err := legacyFS.ReadFile("legacy/index.html")
			if err != nil {
				http.Error(w, "Failed to read index.html", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(content) //nolint:errcheck,gosec
			return
		}
		http.NotFound(w, r)
	})

	// Serve bundled JavaScript for legacy UI
	s.mux.HandleFunc("/bundle.js", func(w http.ResponseWriter, r *http.Request) {
		content, err := legacyFS.ReadFile("legacy/bundle.js")
		if err != nil {
			http.Error(w, "Failed to read bundle.js", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Write(content) //nolint:errcheck,gosec
	})

	// WASM UI at /wasm
	s.mux.HandleFunc("/wasm", func(w http.ResponseWriter, r *http.Request) {
		content, err := wasmDistFS.ReadFile("dist/index.html")
		if err != nil {
			http.Error(w, "Failed to read WASM index.html", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(content) //nolint:errcheck,gosec
	})

	s.mux.HandleFunc("/main.wasm", func(w http.ResponseWriter, r *http.Request) {
		content, err := wasmDistFS.ReadFile("dist/main.wasm")
		if err != nil {
			http.Error(w, "Failed to read main.wasm", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/wasm")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(content) //nolint:errcheck,gosec
	})

	s.mux.HandleFunc("/wasm_exec.js", func(w http.ResponseWriter, r *http.Request) {
		content, err := wasmDistFS.ReadFile("dist/wasm_exec.js")
		if err != nil {
			http.Error(w, "Failed to read wasm_exec.js", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/javascript")
		w.Write(content) //nolint:errcheck,gosec
	})

	// API endpoint for transport data (with caching)
	s.mux.HandleFunc("/api/transports", s.handleTransports)

	// API endpoint for uptime tracker data
	s.mux.HandleFunc("/api/uptimes", s.handleUptimes)

	// API endpoint for service discovery data (combined proxy, VPN, visor)
	s.mux.HandleFunc("/api/services", s.handleServices)

	// API endpoint for IP-based grouping (when survey data is available)
	s.mux.HandleFunc("/api/ip-groups", s.handleIPGroups)

	// API endpoint for local visor data (transports, routes, traffic stats)
	s.mux.HandleFunc("/api/local-visor", s.handleLocalVisor)

	// Websocket endpoint for live local visor data streaming
	s.mux.HandleFunc("/ws/local-visor", s.handleLocalVisorWS)

	// Health check - available at both /health and /api/health
	// /api/health is used when embedded in other servers that have their own /health
	healthHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Get actual cache file ages in seconds
		tpdAge := s.getCacheAgeSeconds(s.config.CacheFile)
		utAge := s.getCacheAgeSeconds(s.config.CacheFileUT)
		sdAge := s.getCacheAgeSeconds(s.config.CacheFileSD)

		// Find the oldest cache age
		maxAge := tpdAge
		if utAge > maxAge {
			maxAge = utAge
		}
		if sdAge > maxAge {
			maxAge = sdAge
		}

		// Calculate seconds until next refresh
		maxAgeSeconds := s.config.CacheMaxAge * 60
		nextRefreshIn := maxAgeSeconds - int(maxAge)
		if nextRefreshIn < 0 {
			nextRefreshIn = 0
		}

		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"status":          "ok",
			"cache_max_age":   s.config.CacheMaxAge,
			"next_refresh_in": nextRefreshIn,
			"cache_ages": map[string]int{
				"tpd": int(tpdAge),
				"ut":  int(utAge),
				"sd":  int(sdAge),
			},
			"auto_refresh": s.config.AutoRefresh,
		})
	}
	s.mux.HandleFunc("/health", healthHandler)
	s.mux.HandleFunc("/api/health", healthHandler)

	// TPS (Transport Setup Node) endpoints — functional only when embedded TPS is running
	s.mux.HandleFunc("/api/tps/status", s.handleTPSStatus)
	s.mux.HandleFunc("/api/tps/add-transport", s.handleTPSAddTransport)
	s.mux.HandleFunc("/api/tps/remove-transport", s.handleTPSRemoveTransport)
	s.mux.HandleFunc("/api/tps/refresh-transports", s.handleTPSRefreshTransports)

	// Local visor transport endpoints — direct control of the local visor's transports
	s.mux.HandleFunc("/api/local/add-transport", s.handleLocalAddTransport)
	s.mux.HandleFunc("/api/local/remove-transport", s.handleLocalRemoveTransport)

	// DMSG discovery endpoints
	s.mux.HandleFunc("/api/dmsg/servers", s.handleDMSGServers)
	s.mux.HandleFunc("/api/dmsg/entries", s.handleDMSGEntries)
	s.mux.HandleFunc("/api/dmsg/health", s.handleDMSGHealth)

	// Network ping endpoint
	s.mux.HandleFunc("/api/ping", s.handlePing)

	// App management endpoints
	s.mux.HandleFunc("/api/apps", s.handleApps)
	s.mux.HandleFunc("/api/apps/start", s.handleAppStart)
	s.mux.HandleFunc("/api/apps/stop", s.handleAppStop)
	s.mux.HandleFunc("/api/apps/autostart", s.handleAppAutoStart)
	s.mux.HandleFunc("/api/apps/set-pk", s.handleAppSetPK)
}

func (s *Server) handleTransports(w http.ResponseWriter, r *http.Request) {
	data, err := s.getData(s.config.CacheFile, s.config.TPDURL+"/all-transports")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch data: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write([]byte(data)) //nolint:errcheck,gosec
}

func (s *Server) handleUptimes(w http.ResponseWriter, r *http.Request) {
	data, err := s.getData(s.config.CacheFileUT, s.config.UTURL+"/uptimes?v=v2")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch uptime data: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write([]byte(data)) //nolint:errcheck,gosec
}

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	// Try to use cached SD data first
	if s.config.CacheFileSD != "" && !s.config.NoCache {
		data, err := s.getSDData()
		if err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Write([]byte(data)) //nolint:errcheck,gosec
			return
		}
	}

	// Fallback: Fetch all service types and combine them
	services := make(map[string]ServiceInfo)

	// Fetch proxy services
	proxyData, err := fetchURL(s.config.SDURL + "/api/services?type=" + servicedisc.ServiceTypeProxy)
	if err == nil {
		s.parseServices(proxyData, "proxy", services)
	}

	// Fetch VPN services
	vpnData, err := fetchURL(s.config.SDURL + "/api/services?type=" + servicedisc.ServiceTypeVPN)
	if err == nil {
		s.parseServices(vpnData, "vpn", services)
	}

	// Fetch visor services
	visorData, err := fetchURL(s.config.SDURL + "/api/services?type=" + servicedisc.ServiceTypeVisor)
	if err == nil {
		s.parseServices(visorData, "visor", services)
	}

	// Convert to JSON
	result, err := json.Marshal(services)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal services: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write(result) //nolint:errcheck,gosec
}

// handleIPGroups returns cached anonymized IP-based groupings of visors
func (s *Server) handleIPGroups(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	s.ipGroupsMu.RLock()
	cache := s.ipGroupsCache
	s.ipGroupsMu.RUnlock()

	if cache == nil {
		// No cache yet, return disabled
		json.NewEncoder(w).Encode(&ipGroupsResponse{ //nolint:errcheck,gosec
			Enabled: false,
			Groups:  map[string]int{},
		})
		return
	}

	json.NewEncoder(w).Encode(cache) //nolint:errcheck,gosec
}

// refreshIPGroupsCache reads survey files, DMSG server data, and local visor data
// to build IP-based groupings. This is called periodically along with other cache refreshes.
func (s *Server) refreshIPGroupsCache() {
	// Map from IP address to group ID
	ipToGroup := make(map[string]int)
	// Map from public key (or node ID) to group ID
	pkToGroup := make(map[string]int)
	nextGroupID := 1

	// Walk the survey directory looking for node-info.json files
	// Expected structure: SurveyDir/<pk>/node-info.json
	if s.config.SurveyDir != "" {
		entries, err := os.ReadDir(s.config.SurveyDir)
		if err != nil {
			s.log.WithError(err).Warn("Failed to read survey directory")
		} else {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}

				surveyFile := filepath.Join(s.config.SurveyDir, entry.Name(), "node-info.json")
				data, err := os.ReadFile(surveyFile) //nolint:gosec
				if err != nil {
					continue // Skip if no survey file for this PK
				}

				var survey surveyData
				if err := json.Unmarshal(data, &survey); err != nil {
					continue // Skip malformed files
				}

				// Skip entries without IP address
				if survey.IPAddr == "" {
					continue
				}

				// Use the public key from the survey if available, otherwise use directory name
				pk := survey.PubKey
				if pk == "" {
					pk = entry.Name()
				}

				// Get or create group ID for this IP
				groupID, exists := ipToGroup[survey.IPAddr]
				if !exists {
					groupID = nextGroupID
					ipToGroup[survey.IPAddr] = groupID
					nextGroupID++
				}

				pkToGroup[pk] = groupID
			}
		}
	}

	// Add local visor to the correct IP group based on its geoip IP
	s.localGeoMu.RLock()
	localIP := ""
	if s.localGeo != nil {
		localIP = s.localGeo.IP
	}
	s.localGeoMu.RUnlock()

	s.visorMu.RLock()
	localPK := ""
	if s.visorCache != nil {
		localPK = s.visorCache.PubKey
	}
	s.visorMu.RUnlock()

	if localIP != "" && localPK != "" {
		// Check if this IP already has a group
		if groupID, exists := ipToGroup[localIP]; exists {
			pkToGroup[localPK] = groupID
			s.log.WithField("pk", localPK[:16]).WithField("group", groupID).WithField("ip", localIP).
				Info("Local visor added to existing IP group")
		} else {
			// Create a new group for this IP
			groupID := nextGroupID
			ipToGroup[localIP] = groupID
			pkToGroup[localPK] = groupID
			s.log.WithField("pk", localPK[:16]).WithField("group", groupID).WithField("ip", localIP).
				Info("Local visor added to new IP group")
		}
	}

	// Add DMSG servers to IP groups using their known IP addresses.
	// Node IDs use the "dmsg-srv-" prefix to match the JS client.
	s.dmsgMu.RLock()
	dmsgCache := s.dmsgCache
	s.dmsgMu.RUnlock()

	if dmsgCache != nil {
		for _, srv := range dmsgCache.Servers {
			if srv.IP == "" || srv.PK == "" {
				continue
			}
			nodeID := "dmsg-srv-" + srv.PK
			groupID, exists := ipToGroup[srv.IP]
			if !exists {
				groupID = nextGroupID
				ipToGroup[srv.IP] = groupID
				nextGroupID++
			}
			pkToGroup[nodeID] = groupID
		}
	}

	// Count unique groups
	totalGroups := len(ipToGroup)

	// Update cache
	s.ipGroupsMu.Lock()
	s.ipGroupsCache = &ipGroupsResponse{
		Enabled:     totalGroups > 0,
		Groups:      pkToGroup,
		TotalGroups: totalGroups,
	}
	s.ipGroupsMu.Unlock()

	if totalGroups > 0 {
		s.log.WithField("groups", totalGroups).WithField("visors", len(pkToGroup)).
			Info("Refreshed IP groups cache")
	}
}

// surveyData holds the minimal survey fields we need for IP grouping
type surveyData struct {
	PubKey string `json:"public_key"`
	IPAddr string `json:"ip_address"`
}

// getSDData gets service discovery data from cache or fetches fresh
func (s *Server) getSDData() (string, error) {
	s.cacheMu.RLock()
	info, err := os.Stat(s.config.CacheFileSD)
	if err != nil {
		s.cacheMu.RUnlock()
		return "", err
	}

	// Check if cache is too old
	if time.Since(info.ModTime()).Minutes() > float64(s.config.CacheMaxAge) {
		s.cacheMu.RUnlock()
		// Trigger refresh in background
		go s.refreshSDCache()
		// Still return stale data for now
	} else {
		s.cacheMu.RUnlock()
	}

	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	return readFile(s.config.CacheFileSD)
}

// ServiceInfo holds service information for a visor
type ServiceInfo struct {
	PK       string   `json:"pk"`
	Services []string `json:"services"`
	Country  string   `json:"country,omitempty"`
}

type sdEntry struct {
	Address string `json:"address"`
	Geo     struct {
		Country string `json:"country"`
	} `json:"geo"`
}

func (s *Server) parseServices(data, serviceType string, services map[string]ServiceInfo) {
	var entries []sdEntry
	if err := json.Unmarshal([]byte(data), &entries); err != nil {
		return
	}

	for _, e := range entries {
		// Extract PK from address (format: pk:port)
		pk := e.Address
		if idx := bytes.IndexByte([]byte(e.Address), ':'); idx > 0 {
			pk = e.Address[:idx]
		}

		if info, exists := services[pk]; exists {
			info.Services = append(info.Services, serviceType)
			if info.Country == "" && e.Geo.Country != "" {
				info.Country = e.Geo.Country
			}
			services[pk] = info
		} else {
			services[pk] = ServiceInfo{
				PK:       pk,
				Services: []string{serviceType},
				Country:  e.Geo.Country,
			}
		}
	}
}

// getData fetches data from the URL via HTTP or from cached file
func (s *Server) getData(cacheFile, url string) (string, error) {
	if s.config.NoCache || cacheFile == "" {
		return fetchURL(url)
	}

	s.cacheMu.RLock()
	info, err := os.Stat(cacheFile)
	if err != nil {
		s.cacheMu.RUnlock()
		// Cache file doesn't exist, fetch synchronously
		data, err := fetchURL(url)
		if err != nil {
			return "", err
		}
		s.cacheMu.Lock()
		defer s.cacheMu.Unlock()
		if err := os.WriteFile(cacheFile, []byte(data), 0644); err != nil { //nolint:gosec
			s.log.WithError(err).Warn("Failed to write cache file")
		}
		s.log.WithField("file", cacheFile).Debug("Auto-refreshed cache")
		return data, nil
	}

	// Check if cache is too old
	if time.Since(info.ModTime()).Minutes() > float64(s.config.CacheMaxAge) {
		s.cacheMu.RUnlock()
		// Trigger refresh in background, return stale data
		go s.refreshCacheFile(cacheFile, url)
	} else {
		s.cacheMu.RUnlock()
	}

	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	return readFile(cacheFile)
}

// refreshCacheFile refreshes a single cache file in the background
func (s *Server) refreshCacheFile(cacheFile, url string) {
	// Check if file needs refresh
	s.cacheMu.RLock()
	info, err := os.Stat(cacheFile)
	if err == nil && time.Since(info.ModTime()).Minutes() <= float64(s.config.CacheMaxAge) {
		s.cacheMu.RUnlock()
		return // File is fresh enough
	}
	s.cacheMu.RUnlock()

	data, err := fetchURL(url)
	if err != nil {
		s.log.WithField("url", url).WithError(err).Warn("Auto-refresh failed")
		return
	}

	s.cacheMu.Lock()
	if err := os.WriteFile(cacheFile, []byte(data), 0644); err != nil { //nolint:gosec
		s.log.WithField("file", cacheFile).WithError(err).Warn("Failed to write cache file")
	}
	s.cacheMu.Unlock()
	s.log.WithField("file", cacheFile).Debug("Auto-refreshed cache")
}

// refreshCache proactively refreshes all cache files
func (s *Server) refreshCache() {
	urls := map[string]string{
		s.config.CacheFile:   s.config.TPDURL + "/all-transports",
		s.config.CacheFileUT: s.config.UTURL + "/uptimes?v=v2",
	}

	for cacheFile, url := range urls {
		if cacheFile == "" {
			continue
		}

		// Check if file needs refresh
		info, err := os.Stat(cacheFile)
		if err == nil && time.Since(info.ModTime()).Minutes() <= float64(s.config.CacheMaxAge) {
			continue // File is fresh enough
		}

		// Fetch and update
		data, err := fetchURL(url)
		if err != nil {
			s.log.WithField("url", url).WithError(err).Warn("Auto-refresh failed")
			continue
		}

		s.cacheMu.Lock()
		if err := os.WriteFile(cacheFile, []byte(data), 0644); err != nil { //nolint:gosec
			s.log.WithField("file", cacheFile).WithError(err).Warn("Failed to write cache file")
		}
		s.cacheMu.Unlock()
		s.log.WithField("file", cacheFile).Debug("Auto-refreshed cache")
	}

	// Refresh service discovery cache (combined from multiple service types)
	s.refreshSDCache()

	// Refresh DMSG discovery cache (servers, entries, clients + geoip)
	s.refreshDMSGCache()

	// Refresh IP groups cache (from survey files)
	s.refreshIPGroupsCache()
}

// refreshSDCache refreshes the service discovery cache
func (s *Server) refreshSDCache() {
	if s.config.CacheFileSD == "" {
		return
	}

	// Check if file needs refresh
	info, err := os.Stat(s.config.CacheFileSD)
	if err == nil && time.Since(info.ModTime()).Minutes() <= float64(s.config.CacheMaxAge) {
		return // File is fresh enough
	}

	// Fetch all service types and combine them
	services := make(map[string]ServiceInfo)

	// Fetch proxy services
	proxyData, err := fetchURL(s.config.SDURL + "/api/services?type=" + servicedisc.ServiceTypeProxy)
	if err == nil {
		s.parseServices(proxyData, "proxy", services)
	}

	// Fetch VPN services
	vpnData, err := fetchURL(s.config.SDURL + "/api/services?type=" + servicedisc.ServiceTypeVPN)
	if err == nil {
		s.parseServices(vpnData, "vpn", services)
	}

	// Fetch visor services
	visorData, err := fetchURL(s.config.SDURL + "/api/services?type=" + servicedisc.ServiceTypeVisor)
	if err == nil {
		s.parseServices(visorData, "visor", services)
	}

	// Convert to JSON and save
	result, err := json.Marshal(services)
	if err != nil {
		s.log.WithError(err).Warn("Auto-refresh SD failed to marshal")
		return
	}

	s.cacheMu.Lock()
	if err := os.WriteFile(s.config.CacheFileSD, result, 0644); err != nil { //nolint:gosec
		s.log.WithField("file", s.config.CacheFileSD).WithError(err).Warn("Failed to write SD cache file")
	}
	s.cacheMu.Unlock()
	s.log.WithField("file", s.config.CacheFileSD).Debug("Auto-refreshed cache")
}

// refreshDMSGCache refreshes the DMSG discovery cache by calling getDMSGData
// which handles all three sub-fetches (servers, entries, clients) with disk caching and geoip.
func (s *Server) refreshDMSGCache() {
	if s.config.DMSGURL == "" {
		return
	}

	// Check if any DMSG cache file needs refresh
	needsRefresh := false
	for _, f := range []string{s.config.CacheFileDMSGServers, s.config.CacheFileDMSGEntries, s.config.CacheFileDMSGClients} {
		if f == "" {
			continue
		}
		info, err := os.Stat(f)
		if err != nil || time.Since(info.ModTime()).Minutes() > float64(s.config.CacheMaxAge) {
			needsRefresh = true
			break
		}
	}

	if !needsRefresh {
		return
	}

	// Clear the in-memory cache to force getDMSGData to re-fetch
	s.dmsgMu.Lock()
	s.dmsgCache = nil
	s.dmsgMu.Unlock()

	if _, err := s.getDMSGData(); err != nil {
		s.log.WithError(err).Warn("Auto-refresh DMSG failed")
	} else {
		s.log.Debug("Auto-refreshed DMSG cache")
	}
}

// startAutoRefresh starts the auto-refresh goroutine
func (s *Server) startAutoRefresh() {
	if !s.config.AutoRefresh || s.config.CacheMaxAge <= 0 {
		return
	}

	// Refresh slightly before cache expires
	interval := time.Duration(s.config.CacheMaxAge) * time.Minute
	if interval > time.Minute {
		interval = interval - 30*time.Second
	}

	s.autoTick = time.NewTicker(interval)

	go func() {
		for {
			select {
			case <-s.stopChan:
				s.autoTick.Stop()
				return
			case <-s.autoTick.C:
				s.refreshCache()
			}
		}
	}()
}

// getCacheAgeSeconds returns the age of a cache file in seconds
func (s *Server) getCacheAgeSeconds(cacheFile string) int64 {
	if cacheFile == "" {
		return 0
	}
	info, err := os.Stat(cacheFile)
	if err != nil {
		return int64(s.config.CacheMaxAge * 60) // Return max age if file doesn't exist
	}
	return int64(time.Since(info.ModTime()).Seconds())
}

// Handler returns the HTTP handler for the server
func (s *Server) Handler() http.Handler {
	return s.mux
}

// Start initializes the cache and starts auto-refresh without starting the HTTP server.
// Use this when embedding the tpviz server in another HTTP server.
func (s *Server) Start() {
	// Fetch local geoip data (for placing local visor in correct country)
	s.fetchLocalGeoIP()

	// Initial cache population
	s.refreshCache()

	// Initial visor data population (if in embedded mode)
	s.visorMu.RLock()
	embedded := s.embeddedMode
	s.visorMu.RUnlock()
	if embedded {
		s.refreshVisorData()
	}

	// Start auto-refresh
	s.startAutoRefresh()

	// Start websocket broadcast goroutine for local visor data
	go s.startLocalVisorBroadcast()
}

// ListenAndServe starts the HTTP server
func (s *Server) ListenAndServe() error {
	listenAddr := fmt.Sprintf("%s:%d", s.config.Addr, s.config.Port)
	s.log.WithField("addr", "http://"+listenAddr).Info("Starting TPD Visualizer server")
	s.log.WithField("tpd", s.config.CacheFile).WithField("ut", s.config.CacheFileUT).
		WithField("sd", s.config.CacheFileSD).Info("Cache files")
	s.log.WithField("max_age_min", s.config.CacheMaxAge).Debug("Cache max age")
	s.log.WithField("tpd", s.config.TPDURL).WithField("ut", s.config.UTURL).
		WithField("sd", s.config.SDURL).Debug("Service URLs")
	if s.config.AutoRefresh {
		s.log.Info("Auto-refresh: enabled")
	}
	s.log.Info("Local visor data overlay is available when running from the visor (use 'skywire cli tp viz --visor')")

	// Fetch local geoip data (for placing local visor in correct country)
	s.fetchLocalGeoIP()

	// Initial cache population
	s.refreshCache()

	// Start auto-refresh
	s.startAutoRefresh()

	// Start websocket broadcast goroutine for local visor data
	go s.startLocalVisorBroadcast()

	return http.ListenAndServe(listenAddr, s.mux) //nolint:gosec
}

// Stop stops the server and cleans up resources
func (s *Server) Stop() {
	close(s.stopChan)

	// Close all websocket connections
	s.wsClientsMu.Lock()
	for ws := range s.wsClients {
		ws.Close(websocket.StatusGoingAway, "server shutting down") //nolint:errcheck,gosec
	}
	s.wsClients = make(map[*websocket.Conn]struct{})
	s.wsClientsMu.Unlock()

	s.visorMu.Lock()
	if s.visorAPI != nil {
		s.visorAPI.Close() //nolint:errcheck,gosec
	}
	s.visorConn = nil
	s.visorAPI = nil
	s.visorMu.Unlock()
}

// refreshVisorData fetches current data from the visor API (when embedded in visor)
func (s *Server) refreshVisorData() {
	s.visorMu.RLock()
	api := s.visorAPI
	embedded := s.embeddedMode
	s.visorMu.RUnlock()

	if api == nil {
		// Not connected, background maintainer will handle reconnection
		return
	}

	data := &LocalVisorData{
		Connected:   true,
		LastUpdated: time.Now(),
	}

	// Get overview (includes PK and transports)
	overview, err := api.Overview()
	if err != nil {
		// In embedded mode, don't clear the API - it's always valid,
		// just mark as temporarily unavailable (visor might still be initializing)
		// IMPORTANT: Preserve the PubKey from SetVisorAPI so local visor still appears in UI
		if embedded {
			s.visorMu.Lock()
			existingPK := ""
			existingCountry := ""
			existingIP := ""
			if s.visorCache != nil {
				existingPK = s.visorCache.PubKey
				existingCountry = s.visorCache.Country
				existingIP = s.visorCache.IP
			}
			// Get geoip data if not already set
			if existingCountry == "" {
				s.localGeoMu.RLock()
				if s.localGeo != nil {
					existingCountry = s.localGeo.Country
					existingIP = s.localGeo.IP
				}
				s.localGeoMu.RUnlock()
			}
			s.visorCache = &LocalVisorData{
				Connected:   true,
				PubKey:      existingPK,
				Country:     existingCountry,
				IP:          existingIP,
				LastUpdated: time.Now(),
			}
			s.visorMu.Unlock()
			return
		}
		// RPC mode: connection lost, mark as disconnected and close connection
		s.visorMu.Lock()
		if s.visorAPI != nil {
			s.visorAPI.Close() //nolint:errcheck,gosec
		}
		s.visorConn = nil
		s.visorAPI = nil
		s.visorCache = &LocalVisorData{Connected: false, LastUpdated: time.Now()}
		s.visorMu.Unlock()
		return
	}

	data.PubKey = overview.PubKey.String()
	data.RoutesCount = overview.RoutesCount

	// Track total bandwidth for animation
	var totalSent, totalRecv uint64

	for _, tp := range overview.Transports {
		lt := LocalTransport{
			ID:       tp.ID.String(),
			RemotePK: tp.Remote.String(),
			Type:     string(tp.Type),
			Label:    string(tp.Label),
			IsSetup:  tp.IsSetup,
		}
		if tp.Log != nil {
			if tp.Log.RecvBytes != nil {
				lt.RecvBytes = *tp.Log.RecvBytes
				totalRecv += lt.RecvBytes
			}
			if tp.Log.SentBytes != nil {
				lt.SentBytes = *tp.Log.SentBytes
				totalSent += lt.SentBytes
			}
		}

		// Calculate delta from previous snapshot
		tpKey := tp.ID.String()
		if prev, ok := s.prevBandwidth[tpKey]; ok {
			if lt.SentBytes >= prev.sent {
				lt.SentDelta = lt.SentBytes - prev.sent
			}
			if lt.RecvBytes >= prev.recv {
				lt.RecvDelta = lt.RecvBytes - prev.recv
			}
		}
		// Update snapshot
		s.prevBandwidth[tpKey] = bandwidthSnapshot{sent: lt.SentBytes, recv: lt.RecvBytes}

		data.Transports = append(data.Transports, lt)
	}

	// Calculate total deltas
	totalKey := "__total__"
	if prev, ok := s.prevBandwidth[totalKey]; ok {
		if totalSent >= prev.sent {
			data.TotalSentDelta = totalSent - prev.sent
		}
		if totalRecv >= prev.recv {
			data.TotalRecvDelta = totalRecv - prev.recv
		}
	}
	s.prevBandwidth[totalKey] = bandwidthSnapshot{sent: totalSent, recv: totalRecv}

	// Build transport ID -> remote PK lookup map
	transportRemotes := make(map[string]string)
	for _, tp := range overview.Transports {
		transportRemotes[tp.ID.String()] = tp.Remote.String()
	}

	// Fetch routing rules
	rules, err := api.RoutingRules()
	if err == nil {
		for _, rule := range rules {
			summary := rule.Summary()
			if summary == nil {
				continue
			}

			lr := LocalRoute{
				RouteID: uint32(summary.KeyRouteID),
			}

			switch summary.Type.String() {
			case "Consume":
				lr.Type = "consume"
				if summary.ConsumeFields != nil {
					lr.SrcPK = summary.ConsumeFields.RouteDescriptor.SrcPK.String()
					lr.DstPK = summary.ConsumeFields.RouteDescriptor.DstPK.String()
				}
			case "Forward":
				lr.Type = "forward"
				if summary.ForwardFields != nil {
					lr.SrcPK = summary.ForwardFields.RouteDescriptor.SrcPK.String()
					lr.DstPK = summary.ForwardFields.RouteDescriptor.DstPK.String()
					tpID := summary.ForwardFields.NextTID.String()
					lr.TransportID = tpID
					// Look up next hop from transport
					if remote, ok := transportRemotes[tpID]; ok {
						lr.NextHopPK = remote
					}
				}
			case "IntermediaryForward":
				lr.Type = "intermediary"
				if summary.IntermediaryForwardFields != nil {
					tpID := summary.IntermediaryForwardFields.NextTID.String()
					lr.TransportID = tpID
					// Look up next hop from transport
					if remote, ok := transportRemotes[tpID]; ok {
						lr.NextHopPK = remote
					}
				}
			}

			data.Routes = append(data.Routes, lr)
		}
	}

	// Add geoip data if available
	s.localGeoMu.RLock()
	if s.localGeo != nil {
		data.Country = s.localGeo.Country
		data.IP = s.localGeo.IP
	}
	s.localGeoMu.RUnlock()

	s.visorMu.Lock()
	s.visorCache = data
	s.visorMu.Unlock()
}

// handleLocalVisor returns data about the local visor's transports and routes
func (s *Server) handleLocalVisor(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Refresh data on each request (only when visor API is set via SetVisorAPI - embedded mode)
	s.visorMu.RLock()
	hasAPI := s.visorAPI != nil
	s.visorMu.RUnlock()

	if hasAPI {
		s.refreshVisorData()
	}

	s.visorMu.RLock()
	cache := s.visorCache
	s.visorMu.RUnlock()

	if cache == nil {
		cache = &LocalVisorData{Connected: false}
	}

	json.NewEncoder(w).Encode(cache) //nolint:errcheck,gosec
}

// handleLocalVisorWS handles websocket connections for live local visor data streaming
func (s *Server) handleLocalVisorWS(w http.ResponseWriter, r *http.Request) {
	// Accept websocket connection
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"}, // Allow any origin for local development
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to accept websocket: %v", err), http.StatusBadRequest)
		return
	}
	defer ws.Close(websocket.StatusNormalClosure, "closed") //nolint:errcheck

	// Register client
	s.wsClientsMu.Lock()
	s.wsClients[ws] = struct{}{}
	s.wsClientsMu.Unlock()

	// Unregister on disconnect
	defer func() {
		s.wsClientsMu.Lock()
		delete(s.wsClients, ws)
		s.wsClientsMu.Unlock()
	}()

	// Send initial data immediately
	s.visorMu.RLock()
	hasAPI := s.visorAPI != nil
	s.visorMu.RUnlock()

	if hasAPI {
		s.refreshVisorData()
	}

	s.visorMu.RLock()
	cache := s.visorCache
	s.visorMu.RUnlock()

	if cache == nil {
		cache = &LocalVisorData{Connected: false}
	}

	initialData, err := json.Marshal(cache)
	if err == nil {
		ws.Write(r.Context(), websocket.MessageText, initialData) //nolint:errcheck,gosec
	}

	// Keep connection alive and handle incoming messages (heartbeat)
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopChan:
			return
		default:
			// Read heartbeat or close signal from client
			_, _, err := ws.Read(ctx)
			if err != nil {
				return
			}
		}
	}
}

// handleTPSStatus returns the status of the embedded Transport Setup Node.
func (s *Server) handleTPSStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	s.tpsMu.RLock()
	api := s.tpsAPI
	s.tpsMu.RUnlock()

	status := map[string]interface{}{
		"running": api != nil,
	}
	if api != nil {
		status["tps_pk"] = api.PK()
	}
	json.NewEncoder(w).Encode(status) //nolint:errcheck,gosec
}

// handleTPSAddTransport creates a transport between two remote visors via TPS RPC.
func (s *Server) handleTPSAddTransport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	s.tpsMu.RLock()
	api := s.tpsAPI
	s.tpsMu.RUnlock()
	if api == nil {
		http.Error(w, `{"error":"TPS not running"}`, http.StatusServiceUnavailable)
		return
	}

	var req struct {
		TargetPK string `json:"target_pk"`
		RemotePK string `json:"remote_pk"`
		Type     string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.Type != "stcpr" && req.Type != "sudph" {
		http.Error(w, `{"error":"only stcpr and sudph transport types are supported"}`, http.StatusBadRequest)
		return
	}
	if req.TargetPK == "" || req.RemotePK == "" {
		http.Error(w, `{"error":"target_pk and remote_pk are required"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	s.log.WithField("target", req.TargetPK).WithField("remote", req.RemotePK).
		WithField("type", req.Type).Info("TPS: creating transport")

	res, err := api.AddTransport(ctx, req.TargetPK, req.RemotePK, req.Type)
	if err != nil {
		s.log.WithError(err).Warn("TPS: failed to create transport")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}) //nolint:errcheck,gosec
		return
	}

	json.NewEncoder(w).Encode(res) //nolint:errcheck,gosec
}

// handleTPSRemoveTransport removes a transport from a remote visor via TPS RPC.
func (s *Server) handleTPSRemoveTransport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	s.tpsMu.RLock()
	api := s.tpsAPI
	s.tpsMu.RUnlock()
	if api == nil {
		http.Error(w, `{"error":"TPS not running"}`, http.StatusServiceUnavailable)
		return
	}

	var req struct {
		TargetPK string `json:"target_pk"`
		ID       string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	s.log.WithField("target", req.TargetPK).WithField("id", req.ID).Info("TPS: removing transport")

	if err := api.RemoveTransport(ctx, req.TargetPK, req.ID); err != nil {
		s.log.WithError(err).Warn("TPS: failed to remove transport")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}) //nolint:errcheck,gosec
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true}) //nolint:errcheck,gosec
}

// handleTPSRefreshTransports queries a remote visor's transport list via TPS RPC.
// This is not instant — it dials the visor via dmsg.
func (s *Server) handleTPSRefreshTransports(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	s.tpsMu.RLock()
	api := s.tpsAPI
	s.tpsMu.RUnlock()
	if api == nil {
		http.Error(w, `{"error":"TPS not running"}`, http.StatusServiceUnavailable)
		return
	}

	pk := r.URL.Query().Get("pk")
	if pk == "" {
		http.Error(w, `{"error":"pk query parameter is required"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	s.log.WithField("target", pk).Info("TPS: refreshing transports from remote visor")

	transports, err := api.GetTransports(ctx, pk)
	if err != nil {
		s.log.WithError(err).Warn("TPS: failed to get transports from remote visor")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}) //nolint:errcheck,gosec
		return
	}

	json.NewEncoder(w).Encode(transports) //nolint:errcheck,gosec
}

// handleLocalAddTransport creates a transport from the local visor to a remote visor.
func (s *Server) handleLocalAddTransport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	s.visorMu.RLock()
	api := s.visorAPI
	s.visorMu.RUnlock()
	if api == nil {
		http.Error(w, `{"error":"local visor not connected"}`, http.StatusServiceUnavailable)
		return
	}

	var req struct {
		RemotePK string `json:"remote_pk"`
		Type     string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.RemotePK == "" {
		http.Error(w, `{"error":"remote_pk is required"}`, http.StatusBadRequest)
		return
	}
	if req.Type == "" {
		req.Type = "sudph"
	}
	if req.Type != "stcpr" && req.Type != "sudph" {
		http.Error(w, `{"error":"type must be stcpr or sudph"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	s.log.WithField("remote_pk", req.RemotePK).WithField("type", req.Type).Info("Local: creating transport")

	tp, err := api.AddTransport(ctx, req.RemotePK, req.Type)
	if err != nil {
		s.log.WithError(err).Warn("Local: failed to create transport")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}) //nolint:errcheck,gosec
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
		"id":        tp.ID.String(),
		"local_pk":  tp.Local.String(),
		"remote_pk": tp.Remote.String(),
		"type":      string(tp.Type),
	})
}

// handleLocalRemoveTransport removes a transport from the local visor.
func (s *Server) handleLocalRemoveTransport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	s.visorMu.RLock()
	api := s.visorAPI
	s.visorMu.RUnlock()
	if api == nil {
		http.Error(w, `{"error":"local visor not connected"}`, http.StatusServiceUnavailable)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.ID == "" {
		http.Error(w, `{"error":"id is required"}`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	s.log.WithField("id", req.ID).Info("Local: removing transport")

	if err := api.RemoveTransport(ctx, req.ID); err != nil {
		s.log.WithError(err).Warn("Local: failed to remove transport")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}) //nolint:errcheck,gosec
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true}) //nolint:errcheck,gosec
}

// broadcastLocalVisorData broadcasts the current local visor data to all connected websocket clients
func (s *Server) broadcastLocalVisorData() {
	s.visorMu.RLock()
	cache := s.visorCache
	s.visorMu.RUnlock()

	if cache == nil {
		cache = &LocalVisorData{Connected: false}
	}

	data, err := json.Marshal(cache)
	if err != nil {
		return
	}

	s.wsClientsMu.RLock()
	clients := make([]*websocket.Conn, 0, len(s.wsClients))
	for ws := range s.wsClients {
		clients = append(clients, ws)
	}
	s.wsClientsMu.RUnlock()

	// Send to all clients with a timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, ws := range clients {
		err := ws.Write(ctx, websocket.MessageText, data)
		if err != nil {
			// Client disconnected, will be cleaned up when their read loop exits
			continue
		}
	}
}

// startLocalVisorBroadcast starts the periodic broadcast of local visor data to websocket clients
func (s *Server) startLocalVisorBroadcast() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			// Only broadcast if there are connected clients and visor API is available
			s.wsClientsMu.RLock()
			hasClients := len(s.wsClients) > 0
			s.wsClientsMu.RUnlock()

			if !hasClients {
				continue
			}

			s.visorMu.RLock()
			hasAPI := s.visorAPI != nil
			s.visorMu.RUnlock()

			if hasAPI {
				s.refreshVisorData()
				s.broadcastLocalVisorData()
			}
		}
	}
}

func fetchURL(url string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url) //nolint:gosec
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck,gosec

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, resp.Body); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func readFile(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// handleDMSGServers returns DMSG server data with geolocation
func (s *Server) handleDMSGServers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	data, err := s.getDMSGData()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(data) //nolint:errcheck,gosec
}

// handleDMSGEntries returns DMSG entries count and list
func (s *Server) handleDMSGEntries(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	data, err := s.getDMSGData()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
		"count":   data.EntriesCount,
		"entries": data.Entries,
	})
}

// handleDMSGHealth performs a health check on a remote visor via DMSG
func (s *Server) handleDMSGHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	pk := r.URL.Query().Get("pk")
	if pk == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": "pk parameter required",
		})
		return
	}

	s.visorMu.RLock()
	visorAPI := s.visorAPI
	s.visorMu.RUnlock()

	if visorAPI == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": "visor not connected - DMSG health check requires visor API",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	resp, err := visorAPI.DMSGHealth(ctx, pk)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(resp) //nolint:errcheck,gosec
}

// handlePing performs a network ping to a remote visor via routes or DMSG.
// Query params: pk (required), mode (route/dmsg, default: dmsg), tries (default: 3), size (KB, default: 2), local (bool, default: false)
func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	pk := r.URL.Query().Get("pk")
	if pk == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"status": "error",
			"error":  "pk parameter required",
		})
		return
	}

	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "dmsg"
	}
	useDMSG := mode == "dmsg"

	// localRoute: use cached TPD data for route calculation instead of route finder
	localRoute := r.URL.Query().Get("local") == "true"

	tries := 3
	if t := r.URL.Query().Get("tries"); t != "" {
		if parsed, err := fmt.Sscanf(t, "%d", &tries); err != nil || parsed != 1 || tries < 1 {
			tries = 3
		}
		if tries > 10 {
			tries = 10
		}
	}

	sizeKB := 2
	if sz := r.URL.Query().Get("size"); sz != "" {
		if parsed, err := fmt.Sscanf(sz, "%d", &sizeKB); err != nil || parsed != 1 || sizeKB < 1 {
			sizeKB = 2
		}
		if sizeKB > 32 {
			sizeKB = 32
		}
	}

	s.visorMu.RLock()
	visorAPI := s.visorAPI
	s.visorMu.RUnlock()

	if visorAPI == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"status": "error",
			"error":  "visor not connected - ping requires visor API",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	resp, err := visorAPI.Ping(ctx, pk, useDMSG, localRoute, tries, sizeKB)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"status": "error",
			"error":  err.Error(),
			"mode":   mode,
		})
		return
	}

	json.NewEncoder(w).Encode(resp) //nolint:errcheck,gosec
}

// handleApps returns the list of all apps and their status.
func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	s.visorMu.RLock()
	visorAPI := s.visorAPI
	s.visorMu.RUnlock()

	if visorAPI == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": "visor not connected",
		})
		return
	}

	apps, err := visorAPI.Apps()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(apps) //nolint:errcheck,gosec
}

// handleAppStart starts an application.
// POST with JSON body: {"name": "app-name"}
func (s *Server) handleAppStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": "POST required",
		})
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": "invalid request body",
		})
		return
	}

	s.visorMu.RLock()
	visorAPI := s.visorAPI
	s.visorMu.RUnlock()

	if visorAPI == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": "visor not connected",
		})
		return
	}

	if err := visorAPI.StartApp(req.Name); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
		"status": "started",
		"app":    req.Name,
	})
}

// handleAppStop stops an application.
// POST with JSON body: {"name": "app-name"}
func (s *Server) handleAppStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": "POST required",
		})
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": "invalid request body",
		})
		return
	}

	s.visorMu.RLock()
	visorAPI := s.visorAPI
	s.visorMu.RUnlock()

	if visorAPI == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": "visor not connected",
		})
		return
	}

	if err := visorAPI.StopApp(req.Name); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
		"status": "stopped",
		"app":    req.Name,
	})
}

// handleAppAutoStart sets the auto-start flag for an app.
// POST with JSON body: {"name": "app-name", "auto_start": true}
func (s *Server) handleAppAutoStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": "POST required",
		})
		return
	}

	var req struct {
		Name      string `json:"name"`
		AutoStart bool   `json:"auto_start"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": "invalid request body",
		})
		return
	}

	s.visorMu.RLock()
	visorAPI := s.visorAPI
	s.visorMu.RUnlock()

	if visorAPI == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": "visor not connected",
		})
		return
	}

	if err := visorAPI.SetAutoStart(req.Name, req.AutoStart); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
		"status":     "updated",
		"app":        req.Name,
		"auto_start": req.AutoStart,
	})
}

// handleAppSetPK sets the server public key for an app (vpn-client, skysocks-client).
// POST with JSON body: {"name": "app-name", "pk": "public-key"}
func (s *Server) handleAppSetPK(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": "POST required",
		})
		return
	}

	var req struct {
		Name string `json:"name"`
		PK   string `json:"pk"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": "invalid request body",
		})
		return
	}

	s.visorMu.RLock()
	visorAPI := s.visorAPI
	s.visorMu.RUnlock()

	if visorAPI == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": "visor not connected",
		})
		return
	}

	if err := visorAPI.SetAppPK(req.Name, req.PK); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"error": err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
		"status": "updated",
		"app":    req.Name,
		"pk":     req.PK,
	})
}

// getDMSGData fetches and caches DMSG discovery data.
// Uses disk caching for each sub-fetch (servers, entries, clients) and in-memory caching
// for the combined DMSGData result.
func (s *Server) getDMSGData() (*DMSGData, error) {
	s.dmsgMu.RLock()
	if s.dmsgCache != nil && time.Since(s.dmsgCache.LastUpdated) < time.Duration(s.config.CacheMaxAge)*time.Minute {
		defer s.dmsgMu.RUnlock()
		return s.dmsgCache, nil
	}
	s.dmsgMu.RUnlock()

	// Fetch fresh data
	s.dmsgMu.Lock()
	defer s.dmsgMu.Unlock()

	// Double-check after acquiring write lock
	if s.dmsgCache != nil && time.Since(s.dmsgCache.LastUpdated) < time.Duration(s.config.CacheMaxAge)*time.Minute {
		return s.dmsgCache, nil
	}

	dmsgData := &DMSGData{
		LastUpdated: time.Now(),
	}

	// Fetch servers (with disk caching)
	serversJSON, err := s.getDMSGSubData(s.config.CacheFileDMSGServers, s.config.DMSGURL+"/dmsg-discovery/all_servers")
	if err != nil {
		return nil, fmt.Errorf("fetch servers: %w", err)
	}

	var serverEntries []struct {
		Static string `json:"static"`
		Server *struct {
			Address           string `json:"address"`
			AvailableSessions int    `json:"availableSessions"`
			ServerType        string `json:"serverType"`
		} `json:"server"`
	}
	if err := json.Unmarshal([]byte(serversJSON), &serverEntries); err != nil {
		return nil, fmt.Errorf("decode servers: %w", err)
	}

	// Convert to DMSGServer and fetch geolocation
	for _, entry := range serverEntries {
		if entry.Server == nil {
			continue
		}
		srv := DMSGServer{
			PK:                entry.Static,
			Address:           entry.Server.Address,
			AvailableSessions: entry.Server.AvailableSessions,
			ServerType:        entry.Server.ServerType,
		}

		// Extract IP from address (format: ip:port)
		if host, _, err := net.SplitHostPort(entry.Server.Address); err == nil {
			srv.IP = host
			// Fetch geolocation
			if geo, err := s.fetchGeoForIP(host); err == nil {
				srv.Country = geo
			}
		}

		dmsgData.Servers = append(dmsgData.Servers, srv)
	}

	// Fetch entries (with disk caching)
	entriesJSON, err := s.getDMSGSubData(s.config.CacheFileDMSGEntries, s.config.DMSGURL+"/dmsg-discovery/entries")
	if err != nil {
		s.log.WithError(err).Warn("Failed to fetch DMSG entries")
	} else {
		var entries []string
		if err := json.Unmarshal([]byte(entriesJSON), &entries); err == nil {
			dmsgData.Entries = entries
			dmsgData.EntriesCount = len(entries)
		}
	}

	// Fetch clients by server (with disk caching, fault-tolerant)
	clientsJSON, err := s.getDMSGSubData(s.config.CacheFileDMSGClients, s.config.DMSGURL+"/dmsg-discovery/servers/clients")
	if err != nil {
		s.log.WithError(err).Debug("Failed to fetch clients by server (endpoint may not be available)")
	} else {
		var clientsByServer map[string][]string
		if err := json.Unmarshal([]byte(clientsJSON), &clientsByServer); err == nil {
			// Map clients to their servers
			for i := range dmsgData.Servers {
				serverPK := dmsgData.Servers[i].PK
				if clients, ok := clientsByServer[serverPK]; ok {
					dmsgData.Servers[i].Clients = append(dmsgData.Servers[i].Clients, clients...)
				}
			}
			s.log.WithField("servers_with_clients", len(clientsByServer)).Debug("Loaded DMSG clients by server")
		}
	}

	s.dmsgCache = dmsgData
	return dmsgData, nil
}

// getDMSGSubData fetches a DMSG sub-endpoint with disk caching.
// Mirrors the getData() pattern: fresh cache → return cached; stale → return stale + background refresh; missing → fetch synchronously.
func (s *Server) getDMSGSubData(cacheFile, url string) (string, error) {
	if s.config.NoCache || cacheFile == "" {
		return fetchURL(url)
	}

	s.cacheMu.RLock()
	info, err := os.Stat(cacheFile)
	if err != nil {
		s.cacheMu.RUnlock()
		// Cache file doesn't exist, fetch synchronously
		data, err := fetchURL(url)
		if err != nil {
			return "", err
		}
		s.cacheMu.Lock()
		defer s.cacheMu.Unlock()
		if err := os.WriteFile(cacheFile, []byte(data), 0644); err != nil { //nolint:gosec
			s.log.WithError(err).Warn("Failed to write DMSG cache file")
		}
		s.log.WithField("file", cacheFile).Debug("Wrote DMSG cache file")
		return data, nil
	}

	// Check if cache is too old
	if time.Since(info.ModTime()).Minutes() > float64(s.config.CacheMaxAge) {
		s.cacheMu.RUnlock()
		// Trigger refresh in background, return stale data
		go s.refreshCacheFile(cacheFile, url)
	} else {
		s.cacheMu.RUnlock()
	}

	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	return readFile(cacheFile)
}

// fetchGeoForIP fetches country code for an IP address
func (s *Server) fetchGeoForIP(ip string) (string, error) {
	if s.config.GeoIPURL == "" {
		return "", fmt.Errorf("geoip URL not configured")
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(s.config.GeoIPURL + "?ip=" + ip)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck

	var geoData struct {
		Country string `json:"country_code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&geoData); err != nil {
		return "", err
	}
	return geoData.Country, nil
}

// NavLink represents a navigation link
type NavLink struct {
	URL   string
	Label string
}

// GetEmbeddedIndexWithNavLinks returns the full embedded legacy index.html with navigation links.
// This is for the legacy JavaScript UI.
func GetEmbeddedIndexWithNavLinks(navLinks []NavLink) (string, error) {
	content, err := legacyFS.ReadFile("legacy/index.html")
	if err != nil {
		return "", err
	}

	html := string(content)

	// Inject nav links before the <h1> tag if any are provided
	if len(navLinks) > 0 {
		navLinksHTML := `<div class="nav-links" style="padding:10px 0;border-bottom:1px solid #0f3460;margin-bottom:15px;">`
		for _, link := range navLinks {
			navLinksHTML += fmt.Sprintf(`<a href="%s" style="color:#3399FF;margin-right:10px;text-decoration:none;font-size:0.85em;">%s</a>`, link.URL, link.Label)
		}
		navLinksHTML += `</div>`

		// Insert before the <h1> tag
		html = strings.Replace(html, `<h1>TPD Visualizer</h1>`, navLinksHTML+`<h1>TPD Visualizer</h1>`, 1)
	}

	return html, nil
}
