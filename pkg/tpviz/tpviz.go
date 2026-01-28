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
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck,gosec
			"status":       "ok",
			"cache_file":   s.config.CacheFile,
			"cache_age":    s.config.CacheMaxAge,
			"tpd_url":      s.config.TPDURL,
			"ut_url":       s.config.UTURL,
			"sd_url":       s.config.SDURL,
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

// Handler returns the HTTP handler for the server
func (s *Server) Handler() http.Handler {
	return s.mux
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

// GetTransportGraphHTML returns the HTML template for embedding in other servers.
// The tpdURL parameter specifies the TPD endpoint to fetch data from.
// If empty, it uses the default production URL.
func GetTransportGraphHTML(tpdURL string) string {
	if tpdURL == "" {
		tpdURL = deployment.Prod.TransportDiscovery + "/all-transports"
	}
	return generateHTML(tpdURL, nil)
}

// GetTransportGraphHTMLWithNavLinks returns the HTML template with custom navigation links.
func GetTransportGraphHTMLWithNavLinks(tpdURL string, navLinks []NavLink) string {
	if tpdURL == "" {
		tpdURL = deployment.Prod.TransportDiscovery + "/all-transports"
	}
	return generateHTML(tpdURL, navLinks)
}

// NavLink represents a navigation link
type NavLink struct {
	URL   string
	Label string
}

func generateHTML(tpdURL string, navLinks []NavLink) string {
	navLinksHTML := ""
	if len(navLinks) > 0 {
		navLinksHTML = `<div class="nav-links">`
		for _, link := range navLinks {
			navLinksHTML += fmt.Sprintf(`<a href="%s">%s</a>`, link.URL, link.Label)
		}
		navLinksHTML += `</div>`
	}

	result := strings.Replace(htmlTemplate, "{{NAV_LINKS}}", navLinksHTML, 1)
	result = strings.Replace(result, "{{TPD_URL}}", tpdURL, 1)
	return result
}

const htmlTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Skywire Transport Discovery Visualizer</title>
    <script src="https://unpkg.com/vis-network/standalone/umd/vis-network.min.js"></script>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif; background: #1a1a2e; color: #eee; }
        #container { display: flex; height: 100vh; }
        #sidebar { width: 300px; background: #16213e; padding: 20px; overflow-y: auto; border-right: 1px solid #0f3460; }
        #network { flex: 1; background: #1a1a2e; }
        h1 { font-size: 1.2em; margin-bottom: 20px; color: #e94560; }
        h2 { font-size: 1em; margin: 15px 0 10px; color: #0f9b8e; }
        .stat { display: flex; justify-content: space-between; padding: 8px 0; border-bottom: 1px solid #0f3460; }
        .stat-label { color: #aaa; }
        .stat-value { font-weight: bold; }
        .legend { margin-top: 15px; }
        .legend-item { display: flex; align-items: center; margin: 8px 0; }
        .legend-color { width: 30px; height: 4px; margin-right: 10px; border-radius: 2px; }
        .stcpr { background: #00d9a5; }
        .sudph { background: #00b4d8; }
        .dmsg { background: #ffd166; }
        .controls { margin-top: 20px; }
        .control-group { margin: 10px 0; }
        label { display: block; margin-bottom: 5px; color: #aaa; font-size: 0.9em; }
        input[type="checkbox"] { margin-right: 8px; }
        input[type="text"] { width: 100%; padding: 8px; background: #0f3460; border: 1px solid #1a1a2e; color: #eee; border-radius: 4px; }
        input[type="text"]:focus { outline: none; border-color: #e94560; }
        button { width: 100%; padding: 10px; margin-top: 10px; background: #e94560; color: white; border: none; border-radius: 4px; cursor: pointer; font-size: 0.9em; }
        button:hover { background: #ff6b6b; }
        button:disabled { background: #555; cursor: not-allowed; }
        #selected-info { margin-top: 20px; padding: 15px; background: #0f3460; border-radius: 4px; display: none; }
        #selected-info.visible { display: block; }
        #selected-pk { font-family: monospace; font-size: 0.7em; word-break: break-all; color: #e94560; margin: 10px 0; }
        .conn-list { max-height: 200px; overflow-y: auto; font-size: 0.8em; }
        .conn-item { padding: 4px 0; border-bottom: 1px solid #1a1a2e; font-family: monospace; font-size: 0.85em; }
        .conn-type { display: inline-block; padding: 2px 6px; border-radius: 3px; font-size: 0.8em; margin-right: 5px; }
        .conn-type.stcpr { background: rgba(0, 217, 165, 0.3); color: #00d9a5; }
        .conn-type.sudph { background: rgba(0, 180, 216, 0.3); color: #00b4d8; }
        .conn-type.dmsg { background: rgba(255, 209, 102, 0.3); color: #ffd166; }
        #loading { position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%); text-align: center; }
        .spinner { width: 50px; height: 50px; border: 3px solid #0f3460; border-top-color: #e94560; border-radius: 50%; animation: spin 1s linear infinite; margin: 0 auto 20px; }
        @keyframes spin { to { transform: rotate(360deg); } }
        #error { color: #e94560; padding: 20px; text-align: center; display: none; }
        .nav-links { padding: 10px 0; border-bottom: 1px solid #0f3460; margin-bottom: 15px; }
        .nav-links a { color: #3399FF; margin-right: 10px; text-decoration: none; font-size: 0.85em; }
        .nav-links a:hover { text-decoration: underline; }
        #cache-info { margin-top: 10px; font-size: 0.8em; color: #666; }
    </style>
</head>
<body>
    <div id="container">
        <div id="sidebar">
            {{NAV_LINKS}}
            <h1>Transport Discovery</h1>
            <h2>Statistics</h2>
            <div class="stat"><span class="stat-label">Total Transports</span><span class="stat-value" id="total-transports">-</span></div>
            <div class="stat"><span class="stat-label">Unique Visors</span><span class="stat-value" id="total-visors">-</span></div>
            <div class="stat"><span class="stat-label">STCPR</span><span class="stat-value" id="count-stcpr">-</span></div>
            <div class="stat"><span class="stat-label">SUDPH</span><span class="stat-value" id="count-sudph">-</span></div>
            <div class="stat"><span class="stat-label">DMSG</span><span class="stat-value" id="count-dmsg">-</span></div>
            <h2>Legend</h2>
            <div class="legend">
                <div class="legend-item"><div class="legend-color stcpr"></div><span>STCPR (TCP)</span></div>
                <div class="legend-item"><div class="legend-color sudph"></div><span>SUDPH (UDP Hole Punch)</span></div>
                <div class="legend-item"><div class="legend-color dmsg"></div><span>DMSG</span></div>
            </div>
            <h2>Filters</h2>
            <div class="controls">
                <div class="control-group">
                    <label><input type="checkbox" id="show-stcpr" checked> Show STCPR</label>
                    <label><input type="checkbox" id="show-sudph" checked> Show SUDPH</label>
                    <label><input type="checkbox" id="show-dmsg" checked> Show DMSG</label>
                </div>
                <div class="control-group">
                    <label for="search">Search PK (first 8 chars)</label>
                    <input type="text" id="search" placeholder="e.g. 027087fe">
                </div>
                <button id="btn-refresh">Refresh Data</button>
                <button id="btn-fit">Fit to Screen</button>
                <div id="cache-info"></div>
            </div>
            <div id="selected-info">
                <h2>Selected Visor</h2>
                <div id="selected-pk"></div>
                <div class="stat"><span class="stat-label">Connections</span><span class="stat-value" id="selected-conn-count">-</span></div>
                <h2>Connected To</h2>
                <div class="conn-list" id="conn-list"></div>
            </div>
        </div>
        <div id="network">
            <div id="loading"><div class="spinner"></div><div>Loading transport data...</div></div>
            <div id="error"></div>
        </div>
    </div>
    <script>
        const TPD_URL = '{{TPD_URL}}';
        let network = null, allData = null, nodesDataset = null, edgesDataset = null, visorConnections = {};
        const colors = { stcpr: '#00d9a5', sudph: '#00b4d8', dmsg: '#ffd166' };

        async function fetchData() {
            document.getElementById('loading').style.display = 'block';
            document.getElementById('error').style.display = 'none';
            try {
                const response = await fetch(TPD_URL);
                if (!response.ok) throw new Error('HTTP ' + response.status);
                allData = await response.json();
                processData();
            } catch (err) {
                document.getElementById('loading').style.display = 'none';
                document.getElementById('error').style.display = 'block';
                document.getElementById('error').textContent = 'Failed to load data: ' + err.message;
            }
        }

        function processData() {
            const visors = new Map();
            visorConnections = {};
            let countStcpr = 0, countSudph = 0, countDmsg = 0;
            allData.forEach(transport => {
                const [pk1, pk2] = transport.edges;
                const type = transport.type;
                if (type === 'stcpr') countStcpr++;
                else if (type === 'sudph') countSudph++;
                else if (type === 'dmsg') countDmsg++;
                [pk1, pk2].forEach(pk => {
                    if (!visors.has(pk)) visors.set(pk, { id: pk, connections: 0, types: new Set() });
                    visors.get(pk).connections++;
                    visors.get(pk).types.add(type);
                });
                if (!visorConnections[pk1]) visorConnections[pk1] = [];
                if (!visorConnections[pk2]) visorConnections[pk2] = [];
                if (pk1 !== pk2) {
                    visorConnections[pk1].push({ pk: pk2, type });
                    visorConnections[pk2].push({ pk: pk1, type });
                }
            });
            document.getElementById('total-transports').textContent = allData.length;
            document.getElementById('total-visors').textContent = visors.size;
            document.getElementById('count-stcpr').textContent = countStcpr;
            document.getElementById('count-sudph').textContent = countSudph;
            document.getElementById('count-dmsg').textContent = countDmsg;
            const maxConn = Math.max(...Array.from(visors.values()).map(v => v.connections));
            const nodes = Array.from(visors.values()).map(v => ({
                id: v.id, label: v.id.substring(0, 8), title: v.id + '\nConnections: ' + v.connections,
                size: 5 + (v.connections / maxConn) * 25,
                color: { background: v.connections > 10 ? '#e94560' : '#4a5568', border: v.connections > 10 ? '#ff6b6b' : '#718096', highlight: { background: '#e94560', border: '#ff6b6b' } },
                connections: v.connections
            }));
            const edges = allData.map((transport, idx) => ({
                id: idx, from: transport.edges[0], to: transport.edges[1],
                color: { color: colors[transport.type], opacity: 0.6 }, type: transport.type, width: 1, smooth: { type: 'continuous' }
            }));
            nodesDataset = new vis.DataSet(nodes);
            edgesDataset = new vis.DataSet(edges);
            createNetwork();
            document.getElementById('loading').style.display = 'none';
        }

        function createNetwork() {
            const container = document.getElementById('network');
            const data = { nodes: nodesDataset, edges: edgesDataset };
            const options = {
                nodes: { shape: 'dot', font: { size: 10, color: '#aaa' }, borderWidth: 2 },
                edges: { smooth: { type: 'continuous', roundness: 0.5 } },
                physics: { stabilization: { iterations: 100, fit: true }, barnesHut: { gravitationalConstant: -3000, springConstant: 0.001, springLength: 200 } },
                interaction: { hover: true, tooltipDelay: 100 }
            };
            network = new vis.Network(container, data, options);
            network.on('click', params => { if (params.nodes.length > 0) showNodeInfo(params.nodes[0]); else hideNodeInfo(); });
            network.on('stabilizationIterationsDone', () => network.fit());
        }

        function showNodeInfo(nodeId) {
            const info = document.getElementById('selected-info');
            const connections = visorConnections[nodeId] || [];
            document.getElementById('selected-pk').textContent = nodeId;
            document.getElementById('selected-conn-count').textContent = connections.length;
            document.getElementById('conn-list').innerHTML = connections.sort((a, b) => a.type.localeCompare(b.type))
                .map(c => '<div class="conn-item"><span class="conn-type ' + c.type + '">' + c.type.toUpperCase() + '</span>' + c.pk.substring(0, 16) + '...</div>').join('');
            info.classList.add('visible');
        }

        function hideNodeInfo() { document.getElementById('selected-info').classList.remove('visible'); }

        function applyFilters() {
            const showStcpr = document.getElementById('show-stcpr').checked;
            const showSudph = document.getElementById('show-sudph').checked;
            const showDmsg = document.getElementById('show-dmsg').checked;
            const searchTerm = document.getElementById('search').value.toLowerCase();
            edgesDataset.forEach(edge => {
                let visible = true;
                if (edge.type === 'stcpr' && !showStcpr) visible = false;
                if (edge.type === 'sudph' && !showSudph) visible = false;
                if (edge.type === 'dmsg' && !showDmsg) visible = false;
                edgesDataset.update({ id: edge.id, hidden: !visible });
            });
            if (searchTerm.length >= 4) {
                nodesDataset.forEach(node => {
                    const matches = node.id.toLowerCase().startsWith(searchTerm);
                    nodesDataset.update({ id: node.id, color: matches ? { background: '#e94560', border: '#ff6b6b' } : { background: node.connections > 10 ? '#e94560' : '#4a5568', border: node.connections > 10 ? '#ff6b6b' : '#718096' } });
                    if (matches) { network.focus(node.id, { scale: 1.5, animation: true }); showNodeInfo(node.id); }
                });
            }
        }

        // Check if running from Go server and show cache info
        async function checkServer() {
            try {
                const resp = await fetch('/health');
                if (resp.ok) {
                    const info = await resp.json();
                    document.getElementById('cache-info').innerHTML = 'Cache: ' + info.cache_file + '<br>Max age: ' + info.cache_age + ' min';
                }
            } catch (e) {
                // Not running from Go server with caching
            }
        }

        document.getElementById('show-stcpr').addEventListener('change', applyFilters);
        document.getElementById('show-sudph').addEventListener('change', applyFilters);
        document.getElementById('show-dmsg').addEventListener('change', applyFilters);
        document.getElementById('search').addEventListener('input', applyFilters);
        document.getElementById('btn-refresh').addEventListener('click', fetchData);
        document.getElementById('btn-fit').addEventListener('click', () => network && network.fit());
        checkServer();
        fetchData();
    </script>
</body>
</html>`
