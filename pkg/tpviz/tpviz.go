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
	"time"

	"github.com/skycoin/skywire/deployment"
)

//go:embed index.html
var embeddedFS embed.FS

// Config holds the configuration for the visualizer server
type Config struct {
	// Addr is the address to bind to (default: 127.0.0.1)
	Addr string
	// Port is the port to listen on (default: 8080)
	Port int
	// CacheFile is the location for the cache file
	CacheFile string
	// CacheMaxAge is the cache max age in minutes (default: 5)
	CacheMaxAge int
	// TPDURL is the transport discovery URL
	TPDURL string
	// NoCache disables caching when true
	NoCache bool
}

// DefaultConfig returns a Config with default values
func DefaultConfig() Config {
	return Config{
		Addr:        "127.0.0.1",
		Port:        8080,
		CacheFile:   filepath.Join(os.TempDir(), "tpviz-cache.json"),
		CacheMaxAge: 5,
		TPDURL:      deployment.Prod.TransportDiscovery,
		NoCache:     false,
	}
}

// Server is the transport visualizer HTTP server
type Server struct {
	config Config
	mux    *http.ServeMux
}

// NewServer creates a new visualizer server with the given config
func NewServer(cfg Config) *Server {
	s := &Server{
		config: cfg,
		mux:    http.NewServeMux(),
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
		w.Write(content) //nolint:errcheck
	})

	// API endpoint for transport data (with caching)
	s.mux.HandleFunc("/api/transports", s.handleTransports)

	// Health check
	s.mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"status":     "ok",
			"cache_file": s.config.CacheFile,
			"cache_age":  s.config.CacheMaxAge,
			"tpd_url":    s.config.TPDURL,
		})
	})
}

func (s *Server) handleTransports(w http.ResponseWriter, r *http.Request) {
	data, err := s.getData()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch data: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write([]byte(data)) //nolint:errcheck
}

// getData fetches data from the TPD URL via HTTP or from cached file
func (s *Server) getData() (string, error) {
	url := s.config.TPDURL + "/all-transports"

	if s.config.NoCache || s.config.CacheFile == "" {
		return fetchURL(url)
	}

	shouldFetch := false

	info, err := os.Stat(s.config.CacheFile)
	if err != nil {
		// Cache file doesn't exist
		shouldFetch = true
	} else {
		// Check if cache is too old
		if time.Since(info.ModTime()).Minutes() > float64(s.config.CacheMaxAge) {
			shouldFetch = true
		}
	}

	if shouldFetch {
		data, err := fetchURL(url)
		if err != nil {
			// If fetch fails but we have a cache file, use it
			if info != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to fetch fresh data, using cache: %v\n", err)
				return readFile(s.config.CacheFile)
			}
			return "", err
		}
		// Write to cache file
		if err := os.WriteFile(s.config.CacheFile, []byte(data), 0644); err != nil { //nolint:gosec
			fmt.Fprintf(os.Stderr, "Warning: Failed to write cache file: %v\n", err)
		}
		return data, nil
	}

	return readFile(s.config.CacheFile)
}

// Handler returns the HTTP handler for the server
func (s *Server) Handler() http.Handler {
	return s.mux
}

// ListenAndServe starts the HTTP server
func (s *Server) ListenAndServe() error {
	listenAddr := fmt.Sprintf("%s:%d", s.config.Addr, s.config.Port)
	fmt.Printf("Starting TPD Visualizer server on http://%s\n", listenAddr)
	fmt.Printf("Cache file: %s (max age: %d minutes)\n", s.config.CacheFile, s.config.CacheMaxAge)
	fmt.Printf("TPD URL: %s\n", s.config.TPDURL)

	return http.ListenAndServe(listenAddr, s.mux) //nolint:gosec
}

func fetchURL(url string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url) //nolint:gosec
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck

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
	data, err := os.ReadFile(path)
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

	return fmt.Sprintf(htmlTemplate, navLinksHTML, tpdURL)
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
        #loading { position: absolute; top: 50%%; left: 50%%; transform: translate(-50%%, -50%%); text-align: center; }
        .spinner { width: 50px; height: 50px; border: 3px solid #0f3460; border-top-color: #e94560; border-radius: 50%%; animation: spin 1s linear infinite; margin: 0 auto 20px; }
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
            %s
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
        const TPD_URL = '%s';
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
