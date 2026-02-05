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
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

//go:embed index.html
var embeddedFS embed.FS

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
	// CacheMaxAge is the cache max age in minutes (default: 5)
	CacheMaxAge int
	// TPDURL is the transport discovery URL
	TPDURL string
	// UTURL is the uptime tracker URL
	UTURL string
	// SDURL is the service discovery URL
	SDURL string
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
		Addr:        "127.0.0.1",
		Port:        8080,
		CacheFile:   filepath.Join(os.TempDir(), "tpd.json"),
		CacheFileUT: filepath.Join(os.TempDir(), "ut.json"),
		CacheFileSD: filepath.Join(os.TempDir(), "sd.json"),
		CacheMaxAge: 5,
		TPDURL:      deployment.Prod.TransportDiscovery,
		UTURL:       deployment.Prod.UptimeTracker,
		SDURL:       deployment.Prod.ServiceDiscovery,
		NoCache:     false,
		AutoRefresh: true,
		GeoIPURL:    "http://ip.skycoin.com",
	}
}

// VisorAPI defines the interface for accessing visor data.
// This is defined locally to avoid import cycles with the visor package.
type VisorAPI interface {
	Overview() (*VisorOverview, error)
	RoutingRules() ([]routing.Rule, error)
	Close() error
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

// Server is the transport visualizer HTTP server
type Server struct {
	config   Config
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
}

// localGeoData holds geoip information for the local visor
type localGeoData struct {
	IP      string `json:"ip"`
	Country string `json:"country_code"`
}

// fetchLocalGeoIP fetches the local visor's IP and country from the geoip service
func (s *Server) fetchLocalGeoIP() {
	if s.config.GeoIPURL == "" {
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(s.config.GeoIPURL)
	if err != nil {
		fmt.Printf("Warning: failed to fetch geoip data: %v\n", err)
		return
	}
	defer resp.Body.Close() //nolint:errcheck

	var geo localGeoData
	if err := json.NewDecoder(resp.Body).Decode(&geo); err != nil {
		fmt.Printf("Warning: failed to parse geoip response: %v\n", err)
		return
	}

	s.localGeoMu.Lock()
	s.localGeo = &geo
	s.localGeoMu.Unlock()

	if geo.Country != "" {
		fmt.Printf("Local visor geoip: IP=%s, Country=%s\n", geo.IP, geo.Country)
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

func (s *Server) setupRoutes() {
	// Serve the embedded index.html at root
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		content, err := embeddedFS.ReadFile("index.html")
		if err != nil {
			http.Error(w, "Failed to read index.html", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
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

// refreshIPGroupsCache reads survey files and updates the cached IP groups data.
// This is called periodically along with other cache refreshes.
func (s *Server) refreshIPGroupsCache() {
	// If no survey dir configured, mark as disabled
	if s.config.SurveyDir == "" {
		s.ipGroupsMu.Lock()
		s.ipGroupsCache = &ipGroupsResponse{
			Enabled: false,
			Groups:  map[string]int{},
		}
		s.ipGroupsMu.Unlock()
		return
	}

	// Map from IP address to group ID
	ipToGroup := make(map[string]int)
	// Map from public key to group ID
	pkToGroup := make(map[string]int)
	nextGroupID := 1

	// Walk the survey directory looking for node-info.json files
	// Expected structure: SurveyDir/<pk>/node-info.json
	entries, err := os.ReadDir(s.config.SurveyDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to read survey directory: %v\n", err)
		s.ipGroupsMu.Lock()
		s.ipGroupsCache = &ipGroupsResponse{
			Enabled: false,
			Groups:  map[string]int{},
		}
		s.ipGroupsMu.Unlock()
		return
	}

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
			fmt.Printf("Local visor %s... added to existing IP group %d (IP: %s)\n",
				localPK[:16], groupID, localIP)
		} else {
			// Create a new group for this IP
			groupID := nextGroupID
			ipToGroup[localIP] = groupID
			pkToGroup[localPK] = groupID
			fmt.Printf("Local visor %s... added to new IP group %d (IP: %s)\n",
				localPK[:16], groupID, localIP)
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
		fmt.Printf("Refreshed IP groups cache: %d groups, %d visors\n", totalGroups, len(pkToGroup))
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
			fmt.Fprintf(os.Stderr, "Warning: Failed to write cache file: %v\n", err)
		}
		fmt.Printf("Auto-refreshed cache: %s\n", cacheFile)
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
		fmt.Fprintf(os.Stderr, "Auto-refresh failed for %s: %v\n", url, err)
		return
	}

	s.cacheMu.Lock()
	if err := os.WriteFile(cacheFile, []byte(data), 0644); err != nil { //nolint:gosec
		fmt.Fprintf(os.Stderr, "Warning: Failed to write cache file %s: %v\n", cacheFile, err)
	}
	s.cacheMu.Unlock()
	fmt.Printf("Auto-refreshed cache: %s\n", cacheFile)
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
			fmt.Fprintf(os.Stderr, "Auto-refresh failed for %s: %v\n", url, err)
			continue
		}

		s.cacheMu.Lock()
		if err := os.WriteFile(cacheFile, []byte(data), 0644); err != nil { //nolint:gosec
			fmt.Fprintf(os.Stderr, "Warning: Failed to write cache file %s: %v\n", cacheFile, err)
		}
		s.cacheMu.Unlock()
		fmt.Printf("Auto-refreshed cache: %s\n", cacheFile)
	}

	// Refresh service discovery cache (combined from multiple service types)
	s.refreshSDCache()

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
		fmt.Fprintf(os.Stderr, "Auto-refresh SD failed to marshal: %v\n", err)
		return
	}

	s.cacheMu.Lock()
	if err := os.WriteFile(s.config.CacheFileSD, result, 0644); err != nil { //nolint:gosec
		fmt.Fprintf(os.Stderr, "Warning: Failed to write SD cache file %s: %v\n", s.config.CacheFileSD, err)
	}
	s.cacheMu.Unlock()
	fmt.Printf("Auto-refreshed cache: %s\n", s.config.CacheFileSD)
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
	fmt.Printf("Starting TPD Visualizer server on http://%s\n", listenAddr)
	fmt.Printf("Cache files:\n")
	fmt.Printf("  TPD: %s\n", s.config.CacheFile)
	fmt.Printf("  UT:  %s\n", s.config.CacheFileUT)
	fmt.Printf("  SD:  %s\n", s.config.CacheFileSD)
	fmt.Printf("Cache max age: %d minutes\n", s.config.CacheMaxAge)
	fmt.Printf("TPD URL: %s\n", s.config.TPDURL)
	fmt.Printf("UT URL:  %s\n", s.config.UTURL)
	fmt.Printf("SD URL:  %s\n", s.config.SDURL)
	if s.config.AutoRefresh {
		fmt.Printf("Auto-refresh: enabled\n")
	}
	fmt.Println("Note: Local visor data overlay is available when running from the visor (use 'skywire cli tp viz --visor')")

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

// NavLink represents a navigation link
type NavLink struct {
	URL   string
	Label string
}

// GetEmbeddedIndexWithNavLinks returns the full embedded index.html with navigation links.
// This is the single source of truth for the transport graph HTML - no duplicate templates.
func GetEmbeddedIndexWithNavLinks(navLinks []NavLink) (string, error) {
	content, err := embeddedFS.ReadFile("index.html")
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
