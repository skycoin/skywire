// Package tpviz provides a web-based transport discovery visualizer
package tpviz

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/servicedisc"
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
	}
}

// Server is the transport visualizer HTTP server
type Server struct {
	config   Config
	mux      *http.ServeMux
	cacheMu  sync.RWMutex
	stopChan chan struct{}
	autoTick *time.Ticker
}

// NewServer creates a new visualizer server with the given config
func NewServer(cfg Config) *Server {
	s := &Server{
		config:   cfg,
		mux:      http.NewServeMux(),
		stopChan: make(chan struct{}),
	}
	s.setupRoutes()
	return s
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

	// Health check
	s.mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
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
	})
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
	shouldFetch := false

	info, err := os.Stat(cacheFile)
	if err != nil {
		// Cache file doesn't exist
		shouldFetch = true
	} else {
		// Check if cache is too old
		if time.Since(info.ModTime()).Minutes() > float64(s.config.CacheMaxAge) {
			shouldFetch = true
		}
	}
	s.cacheMu.RUnlock()

	if shouldFetch {
		s.cacheMu.Lock()
		defer s.cacheMu.Unlock()

		// Double-check after acquiring write lock
		info, err = os.Stat(cacheFile)
		if err == nil && time.Since(info.ModTime()).Minutes() <= float64(s.config.CacheMaxAge) {
			// Another goroutine updated the cache
			return readFile(cacheFile)
		}

		data, err := fetchURL(url)
		if err != nil {
			// If fetch fails but we have a cache file, use it
			if info != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to fetch fresh data, using cache: %v\n", err)
				return readFile(cacheFile)
			}
			return "", err
		}
		// Write to cache file
		if err := os.WriteFile(cacheFile, []byte(data), 0644); err != nil { //nolint:gosec
			fmt.Fprintf(os.Stderr, "Warning: Failed to write cache file: %v\n", err)
		}
		return data, nil
	}

	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	return readFile(cacheFile)
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
	// Initial cache population
	s.refreshCache()

	// Start auto-refresh
	s.startAutoRefresh()
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

	// Initial cache population
	s.refreshCache()

	// Start auto-refresh
	s.startAutoRefresh()

	return http.ListenAndServe(listenAddr, s.mux) //nolint:gosec
}

// Stop stops the server and cleans up resources
func (s *Server) Stop() {
	close(s.stopChan)
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
