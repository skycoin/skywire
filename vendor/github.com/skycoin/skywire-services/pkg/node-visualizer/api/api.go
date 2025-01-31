// Package api pkg/node-visualizer/api/api.go
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/cors"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// API register all the API endpoints.
// It implements a net/http.Handler.
type API struct {
	http.Handler
	startedAt time.Time
}

func (a *API) log(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
}

// BackgroundTask fetch node and edges each 30 seconds
func (a *API) BackgroundTask(utURL, tpdURL string) {
	// Fetch data initially
	for {
		if err := fetchEdges(tpdURL); err != nil {
			fmt.Printf("Error fetching edges: %v\n", err)
			return
		}
		time.Sleep(5 * time.Second)
		if err := fetchNodes(utURL); err != nil {
			fmt.Printf("Error fetching nodes: %v\n", err)
			return
		}
		time.Sleep(30 * time.Second)
	}
}

func (a *API) htmlHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, htmlContent) //nolint: errcheck
}

// HealthCheckResponse is struct of /health endpoint
type HealthCheckResponse struct {
	BuildInfo *buildinfo.Info `json:"build_info,omitempty"`
	StartedAt time.Time       `json:"started_at"`
}

// New constructs a new API instance.
func New(log logrus.FieldLogger) *API {
	if log == nil {
		log = logging.MustGetLogger("node_visulaizer")
	}

	api := &API{
		startedAt: time.Now(),
	}

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(httputil.SetLoggerMiddleware(log))
	r.Use(cors.AllowAll().Handler)

	r.Get("/health", api.health)
	r.Get("/tpd-graph", graphHandler)
	r.Get("/", api.htmlHandler)

	api.Handler = r

	return api
}

// TransportData represents the data structure for a transport node.
type TransportData struct {
	TID   string   `json:"t_id"`
	Edges []string `json:"edges"`
	Type  string   `json:"type"`
	Label string   `json:"label"`
}

// UTData represents the data structure for a uptime tracker data.
type UTData struct {
	Key string `json:"key"`
}

var graphData []TransportData
var utData []UTData
var mu sync.Mutex

// fetchEdges fetches data from an external API
func fetchEdges(tpdURL string) error {
	resp, err := http.Get(tpdURL) //nolint: gosec
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint: errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var data []TransportData
	if err := json.Unmarshal(body, &data); err != nil {
		return err
	}

	// Update global graph data
	mu.Lock()
	graphData = data
	mu.Unlock()

	return nil
}

// fetchNodes fetches data from uptime tracker
func fetchNodes(utURL string) error {
	resp, err := http.Get(utURL) //nolint: gosec
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint: errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var data []UTData
	if err := json.Unmarshal(body, &data); err != nil {
		return err
	}

	// Update global graph data
	mu.Lock()
	utData = data
	mu.Unlock()

	return nil
}

// createGraph creates a graph from the transport data
func createGraph(debug bool) ([]map[string]interface{}, error) {
	mu.Lock()
	defer mu.Unlock()

	var nodes []map[string]interface{}
	var edges []map[string]interface{}
	nodeMap := make(map[string]int64)
	nodeID := int64(0)

	// Create nodes and edges from graph data and ut data
	for _, item := range graphData {
		edgeA := item.Edges[0]
		edgeB := item.Edges[1]
		_, exist := nodeMap[edgeA]
		if !exist {
			nodeMap[edgeA] = nodeID
			nodeID++
			label := ""
			if debug {
				label = edgeA
			}
			nodes = append(nodes, map[string]interface{}{
				"id":    edgeA,
				"label": label,
			})
		}
		_, exist = nodeMap[edgeB]
		if !exist {
			nodeMap[edgeB] = nodeID
			nodeID++
			label := ""
			if debug {
				label = edgeB
			}
			nodes = append(nodes, map[string]interface{}{
				"id":    edgeB,
				"label": label,
			})
		}

		edges = append(edges, map[string]interface{}{
			"from": edgeA,
			"to":   edgeB,
		})
	}

	for _, utItem := range utData {
		_, exist := nodeMap[utItem.Key]
		if !exist {
			nodeMap[utItem.Key] = nodeID
			nodeID++
			label := ""
			if debug {
				label = utItem.Key
			}
			nodes = append(nodes, map[string]interface{}{
				"id":    utItem.Key,
				"label": label,
			})
		}
	}

	// Return nodes and edges as a slice of maps
	return []map[string]interface{}{
		{"nodes": nodes},
		{"edges": edges},
	}, nil
}

// graphHandler serves the graph data as JSON for frontend visualization
func graphHandler(w http.ResponseWriter, r *http.Request) {
	// Set CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*") // Allow all origins (you can specify a specific domain)
	w.Header().Set("Access-Control-Allow-Methods", "GET")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Handle OPTIONS request for preflight (CORS)
	if r.Method == http.MethodOptions {
		return
	}

	var debug bool
	query := r.URL.Query()
	selfTransportsParam := query.Get("debug")
	if selfTransportsParam == "true" {
		debug = true
	}
	// Create the graph
	graph, err := createGraph(debug)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create graph: %v", err), http.StatusInternalServerError)
		return
	}

	// Serve the graph data as JSON
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(graph); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode JSON: %v", err), http.StatusInternalServerError)
	}
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Get()
	a.writeJSON(w, r, http.StatusOK, HealthCheckResponse{
		BuildInfo: info,
		StartedAt: a.startedAt,
	})
}

// writeJSON writes a json object on a http.ResponseWriter with the given code
func (a *API) writeJSON(w http.ResponseWriter, r *http.Request, code int, object interface{}) {
	jsonObject, err := json.Marshal(object)
	if err != nil {
		a.log(r).WithError(err).Errorf("failed to encode json response")
		w.WriteHeader(http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	_, err = w.Write(jsonObject)
	if err != nil {
		a.log(r).WithError(err).Errorf("failed to write json response")
	}
}

// Error is the object returned to the client when there's an error.
type Error struct {
	Error string `json:"error"`
}

const htmlContent = `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Node Visualizer</title>
    <style>
        #graph-container {
            width: 100%;
            height: 600px;
            border: 1px solid lightgray;
        }
    </style>
    <script type="text/javascript" src="https://unpkg.com/vis-network@9.0.0/dist/vis-network.min.js"></script>
</head>
<body>
    Node Visualizer
    <div id="graph-container"></div>
    
    <script type="text/javascript">
        // Fetch the graph data from the Go server
        fetch('/tpd-graph')
            .then(response => response.json())
            .then(data => {
                // Extract nodes and edges
                const nodes = new vis.DataSet(data[0].nodes);
                const edges = new vis.DataSet(data[1].edges);

                // Create a network visualization
                const container = document.getElementById('graph-container');
                const network = new vis.Network(container, { nodes: nodes, edges: edges }, {layout: { improvedLayout: false }});
                
            })
            .catch(error => console.error('Error fetching graph data:', error));
    </script>
</body>
</html>
`
