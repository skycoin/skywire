// Package logserver pkg/visor/logserver/api.go c3-vis-core
package logserver

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/flightrec"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/pty"
	"github.com/skycoin/skywire/pkg/serviceuptime"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/logserver/landingpage"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// ServiceEntry describes a port-forwarded service for the /services catalog.
type ServiceEntry struct {
	Port  uint16 `json:"port"`
	Label string `json:"label"`
}

// ServiceLister provides the service catalog for the /services endpoint.
// Implemented by visor's ServiceRegistry.
type ServiceLister interface {
	ListPublic() []ServiceEntry
}

// ForwardedPortEntry describes a user-forwarded port for the landing page.
type ForwardedPortEntry struct {
	Port        int    `json:"port"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// ForwardedPortLister provides forwarded ports for the landing page
// and per-port whitelist for access control.
type ForwardedPortLister interface {
	LandingPageEntries() []ForwardedPortEntry
	// PortWhitelist returns the PK whitelist for a given port.
	// An empty slice means the port is accessible to everyone.
	PortWhitelist(port int) []cipher.PubKey
}

// dmsgHealthProvider adds the visor's dmsg sessions and the dmsg server it
// runs, if any, to /health.
type dmsgHealthProvider interface {
	DmsgSessionCount() int
	DmsgServerHealth() *httputil.DmsgServerHealth
}

// HealthStatsProvider provides transport statistics for the /health endpoint.
type HealthStatsProvider interface {
	// IsPublicAutoconnectRunning returns true if the public autoconnect module is running.
	IsPublicAutoconnectRunning() bool
	// GetTransportCounts returns the count of STCPR and SUDPH transports (excluding "user" labeled).
	GetTransportCounts() (stcpr, sudph int)
	// GetTransportTypeCounts returns a count of live transports keyed by their
	// actual network type (excluding "user" labeled) — every present type, not
	// just stcpr/sudph.
	GetTransportTypeCounts() map[string]int
	// GetNetworkTypes returns the network types used by the visor.
	GetNetworkTypes() []string
}

// CXOFeedEntry describes one CXO TreeStore feed published by the visor.
// Subscribers connect to (visor PK, DmsgPort) over DMSG and request
// the registered prefix, mirroring the data into their own subscriber.
type CXOFeedEntry struct {
	Name        string `json:"name"`
	DmsgPort    uint16 `json:"dmsg_port"`
	Description string `json:"description,omitempty"`
	System      bool   `json:"system,omitempty"`
}

// RelatedNodesProvider exposes this node's mesh relationships for the
// landing page: the visors a hypervisor manages, and the hypervisors a
// visor is configured to trust. Both are shown ONLY to an authenticated
// (survey-whitelisted, or self-loopback) caller — they reveal the node's
// management topology — and rendered as links to each peer's landing page
// (http://<pk>.dmsg), reachable through a resolving proxy.
//
// Implemented by the visor. A nil provider (or empty slices) renders no
// related-nodes section.
type RelatedNodesProvider interface {
	// ManagedVisors returns the PKs of the visors this node currently
	// manages as a hypervisor (the same set as `hv ls`). Empty when this
	// node is not acting as a hypervisor or has no connected visors.
	ManagedVisors() []cipher.PubKey
	// ConfiguredHypervisors returns the PKs of the hypervisors this visor
	// is configured to be managed by (v.conf.Hypervisors).
	ConfiguredHypervisors() []cipher.PubKey
}

// CXOFeedsLister provides the list of CXO feeds the visor is publishing.
// Returns the always-on system feed (stats/telemetry) plus any
// user-registered feeds. Pure metadata — actual feed content is served
// out-of-band over DMSG by treestore.Publisher.
type CXOFeedsLister interface {
	ListCXOFeeds() []CXOFeedEntry
}

// API register all the API endpoints.
// It implements a net/http.Handler.
type API struct {
	http.Handler

	logger               *logging.Logger
	startedAt            time.Time
	publicKey            string // visor PK hex, shown on the landing page; set via SetIdentity (empty until wired)
	dmsgAddress          string // <pk>:<dmsg-port>, emitted on /health; set via SetIdentity
	healthStatsProvider  HealthStatsProvider
	serviceLister        ServiceLister
	forwardedPortLister  ForwardedPortLister
	cxoFeedsLister       CXOFeedsLister
	relatedNodesProvider RelatedNodesProvider
	statsReader          StatsReader             // visor-local telemetry store, set via SetStatsReader
	uptimeRecorder       *serviceuptime.Recorder // service-self uptime, set via SetUptimeRecorder
	logLevelController   LogLevelController      // temporary log level, set via SetLogLevelController
	websiteHandler       http.Handler            // optional: serves unmatched routes (custom website)
	// statusPage serves /status, the status of the deployment services the
	// visor runs. Nil answers 404.
	statusPage http.Handler
	// ptyHandler serves /pty (web terminal) when set by the visor.
	// Gated by ptyWhitelist — typically the dmsgpty whitelist (configured
	// PKs + hypervisor PKs + the visor's own PK).
	ptyHandler   http.Handler
	ptyWhitelist pty.Whitelist
	// transportListProvider serves GET /transports to callers over a transport.
	transportListProvider TransportListProvider
	// reachCardProvider serves GET /reach, how to dial this visor.
	reachCardProvider ReachCardProvider
	// wasmModulePath is the js/wasm module served under GET /wasm/.
	wasmModulePath string
}

// ptyPKAllowed reports whether the request's remote host (a PK hex
// string) is on the live pty whitelist. Fails closed on a nil
// whitelist or an unparseable host — /pty is high-power.
func ptyPKAllowed(wl pty.Whitelist, remoteHost string) bool {
	if wl == nil {
		return false
	}
	var pk cipher.PubKey
	if err := pk.Set(remoteHost); err != nil {
		return false
	}
	ok, err := wl.Get(pk)
	return err == nil && ok
}

// New creates a new API.
//
// The trailing unused string parameter is legacy: historical bandwidth is
// now served from the bbolt stats store via /stats/transports/history, and
// the on-disk CSV transport-log store (with its /transport_logs/:file route)
// has been removed.
func New(log *logging.Logger, localPath, _ string, whitelistedPKs []cipher.PubKey, survey *visorconfig.Survey, printLog bool) *API {
	api := &API{
		logger:    log,
		startedAt: time.Now(),
	}
	r := http.NewServeMux()

	// whitelist-based authentication for survey collection if there are keys whitelisted for that
	// no survey-whitelisted keys means the file is publicly accessible
	authRoute := func(pattern string, h http.HandlerFunc) {
		if len(whitelistedPKs) > 0 {
			r.Handle(pattern, whitelistAuth(whitelistedPKs, h))
			return
		}
		r.Handle(pattern, h)
	}

	// serve the file with the reward address - only exists if the reward address is set
	rewardFile := filepath.Join(localPath, skyenv.RewardFile)
	authRoute("GET /"+skyenv.RewardFile, func(w http.ResponseWriter, req *http.Request) {
		http.ServeFile(w, req, rewardFile)
	})

	// This survey endpoint generates the survey as a response
	authRoute("GET /node-info", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, *survey)
	})

	// Checksum endpoint for survey — allows collectors to skip re-downloading unchanged surveys
	authRoute("GET /node-info/checksum", func(w http.ResponseWriter, _ *http.Request) {
		data, err := json.Marshal(*survey)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		sum := sha256.Sum256(data)
		writeJSON(w, http.StatusOK, jsonObj{"sha256": hex.EncodeToString(sum[:])})
	})

	r.HandleFunc("GET /health", api.health)
	r.HandleFunc("GET /transports", api.transportList)
	r.HandleFunc("GET /reach", api.reachCard) // addrresolver.ReachPath
	r.HandleFunc("GET /wasm/skywire.wasm.gz", api.wasmModule("skywire.wasm.gz"))
	r.HandleFunc("GET /wasm/skywire.wasm.json", api.wasmModule("skywire.wasm.json"))

	// Service catalog — lists ports available for .skynet / skynet
	// forwarding. Public services are visible; hidden services are
	// omitted. Browsers visiting http://pk.dmsg/services see what
	// the visor exposes.
	r.HandleFunc("GET /services", func(w http.ResponseWriter, _ *http.Request) {
		if api.serviceLister == nil {
			writeJSON(w, http.StatusOK, []ServiceEntry{})
			return
		}
		writeJSON(w, http.StatusOK, api.serviceLister.ListPublic())
	})

	// CXO feed catalog — lists every feed (system + user-registered)
	// that the visor publishes over DMSG. Subscribers fetch this once
	// to discover names + ports, then connect their CXO subscriber to
	// (this visor's PK, dmsg_port) and apply prefix filtering. Pure
	// metadata, no auth required.
	r.HandleFunc("GET /feeds", func(w http.ResponseWriter, _ *http.Request) {
		if api.cxoFeedsLister == nil {
			writeJSON(w, http.StatusOK, []CXOFeedEntry{})
			return
		}
		writeJSON(w, http.StatusOK, api.cxoFeedsLister.ListCXOFeeds())
	})

	// Serve visor log file (auth'd) — written when visor runs with -s/--save-log
	//
	// With no query params: behaves like a static file dump (http.ServeFile).
	//
	// Query params (any set → switches to streaming filtered mode):
	//   ?min-level=<lvl>    keep only lines >= <lvl> (trace<debug<info<warn<error<fatal<panic)
	//   ?module=<regex>     keep only lines whose `[module]` tag matches the regex
	//   ?grep=<regex>       keep only lines whose full text matches the regex
	//   ?since-line=<N>     skip the first N lines (1-based, useful for resume after disconnect)
	//   ?limit=<N>          stop after writing N matching lines
	//   ?follow=1           keep reading after EOF (tail -f), polling for appends until ctx fires
	//
	// Filtering happens server-side so callers over dmsg/skynet don't burn bandwidth
	// pulling everything just to grep locally. The standard line format is
	//   `[<iso8601>] LEVEL [module:...]: msg key=val…`
	// — anything that doesn't match this shape is passed through unchanged when
	// no level/module filters apply, and conservatively dropped when they do (so
	// stray lines from libraries that don't follow the format don't sneak past
	// a strict --min-level filter).
	//
	// Output modes:
	//   default            → terminal-styled HTML (colored per log level),
	//                        STREAMED: renders the backlog then tails the file
	//                        live (chunked) until the client disconnects
	//   ?raw=1 (or Accept   → verbatim plain text (http.ServeFile) for log-scraping
	//     text/plain)         (one-shot — completes for scrapers)
	//   any filter param   → plain-text streaming filtered mode
	//
	// Served at /skywire.log (matching the on-disk filename written by
	// visor.go's lumberjackrus hook) with /visor.log kept as an alias so
	// older links and tooling keep working — both routes share this handler.
	serveVisorLog := func(w http.ResponseWriter, req *http.Request) {
		// The visor writes its rotating log to LocalPath/log/skywire.log
		// (visor.go, via lumberjackrus). The endpoint previously looked at
		// LocalPath/visor.log — wrong subdir AND filename — so it always 404'd
		// even with file logging enabled.
		logFile := filepath.Join(localPath, "log", "skywire.log")
		if _, err := os.Stat(logFile); err != nil {
			writeString(w, http.StatusNotFound, "%s not found (is file logging enabled?)", logFile)
			return
		}
		q := req.URL.Query()
		// Server-side filtering (always plain text — keeps grep/scrape simple).
		if q.Get("min-level") != "" || q.Get("module") != "" || q.Get("grep") != "" ||
			q.Get("since-line") != "" || q.Get("limit") != "" || q.Get("follow") != "" {
			streamFilteredVisorLog(w, req, logFile, q)
			return
		}
		// Plain-text escape hatch for log-scraping tooling: ?raw=1 or an
		// explicit Accept: text/plain (and no text/html preference).
		raw := q.Get("raw") == "1" || strings.EqualFold(q.Get("raw"), "true")
		if !raw {
			accept := req.Header.Get("Accept")
			if strings.Contains(accept, "text/plain") && !strings.Contains(accept, "text/html") {
				raw = true
			}
		}
		if raw {
			http.ServeFile(w, req, logFile)
			return
		}
		// Default: terminal-styled, level-colored HTML.
		renderVisorLogHTML(w, req, logFile)
	}
	authRoute("GET /skywire.log", serveVisorLog)
	authRoute("GET /visor.log", serveVisorLog) // backwards-compatible alias

	// pprof endpoints (auth'd) — runtime profiling
	// The subtree pattern also serves the named profiles, such as /debug/pprof/heap.
	authRoute("GET /debug/pprof/", pprof.Index)
	authRoute("GET /debug/pprof/cmdline", pprof.Cmdline)
	authRoute("GET /debug/pprof/profile", pprof.Profile)
	authRoute("GET /debug/pprof/symbol", pprof.Symbol)
	authRoute("GET /debug/pprof/trace", pprof.Trace)
	// The last seconds of execution trace, when the flight recorder runs.
	authRoute("GET /debug/pprof/flightrecorder", flightrec.Handler().ServeHTTP)
	// A full heap dump written to LocalPath/log, for finding what retains an object.
	authRoute("POST /debug/heapdump", heapDumpHandler(localPath))

	// /stats/* (auth'd) — visor-local telemetry store. Handlers
	// degrade to 503 when SetStatsReader hasn't been called.
	api.registerStatsRoutes(authRoute)

	// /uptime/* (auth'd) — service-self uptime store. Handlers
	// degrade to 503 when SetUptimeRecorder hasn't been called.
	api.registerUptimeRoutes(authRoute)

	// /debug/loglevel (auth'd) — a log level raised for a bounded time.
	// Handlers degrade to 503 when SetLogLevelController hasn't been called.
	api.registerLogLevelRoutes(authRoute)

	// /pty (web terminal) — gated by ptyWhitelist (set via
	// SetPtyHandler). Until the visor calls SetPtyHandler, the
	// route is wired but returns 404 so a misconfigured deployment
	// doesn't accidentally expose a shell. The dmsgpty UI handler
	// terminates websocket-upgrade requests for the live session
	// and serves the static term page on plain GETs.
	ptyAuth := func(w http.ResponseWriter, req *http.Request) {
		if api.ptyHandler == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// Nil whitelist means "no PK allowed" — pty is high-power,
		// fail closed.
		remoteHost, _, err := net.SplitHostPort(req.RemoteAddr)
		if err != nil {
			remoteHost = req.RemoteAddr
		}
		if !ptyPKAllowed(api.ptyWhitelist, remoteHost) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		api.ptyHandler.ServeHTTP(w, req)
	}
	r.HandleFunc("GET /pty", ptyAuth)
	r.HandleFunc("GET /pty/", ptyAuth)

	// isWhitelisted checks if the current request is from a whitelisted PK
	// without blocking. Used by the landing page to show/hide auth'd links.
	isWhitelisted := func(req *http.Request) bool {
		if len(whitelistedPKs) == 0 {
			return true
		}
		remotePK, _, err := net.SplitHostPort(req.RemoteAddr)
		if err != nil {
			return false
		}
		for _, pk := range whitelistedPKs {
			if remotePK == pk.String() {
				return true
			}
		}
		return false
	}

	r.HandleFunc("GET /status", func(w http.ResponseWriter, req *http.Request) {
		if api.statusPage == nil {
			http.NotFound(w, req)
			return
		}
		api.statusPage.ServeHTTP(w, req)
	})

	// Landing page with links to available endpoints. When a custom
	// websiteHandler is set (port 80 reverse-proxy or rewards UI), it
	// serves the root path too — replacing the default landing page
	// rather than only catching unmatched routes.
	r.HandleFunc("GET /{$}", func(w http.ResponseWriter, req *http.Request) {
		if api.websiteHandler != nil {
			api.websiteHandler.ServeHTTP(w, req)
			return
		}
		wl := isWhitelisted(req)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		var links []string
		links = append(links, `<a href="/health">/health</a> - visor health status`)
		if api.statusPage != nil {
			links = append(links, `<a href="/status">/status</a> - deployment services status`)
		}
		if wl {
			links = append(links, `<a href="/node-info">/node-info</a> - node survey`)
			links = append(links, `<a href="/node-info/checksum">/node-info/checksum</a> - survey checksum`)
			links = append(links, `<a href="/skywire.log">/skywire.log</a> - visor debug log`)
			links = append(links, `<a href="/debug/pprof/">/debug/pprof/</a> - runtime profiling`)
			if api.logLevelController != nil {
				links = append(links, `<a href="/debug/loglevel">/debug/loglevel</a> - log level (POST ?level=debug&ttl=15m raises it for a while)`)
			}
			if api.statsReader != nil {
				links = append(links, `<a href="/stats/transports">/stats/transports</a> - live transport snapshot`)
				links = append(links, `<a href="/stats/transports/history">/stats/transports/history</a> - daily transport rollups (?since=&until=&id=)`)
				links = append(links, `<a href="/stats/uptime">/stats/uptime</a> - three-tier uptime bitmaps`)
				links = append(links, `<a href="/stats/services">/stats/services</a> - per-service uptime bitmaps`)
			}
			// /pty link is shown only when the requester's PK is on
			// the dmsgpty whitelist. The pty whitelist is intentionally
			// distinct from the survey whitelist (it can include
			// Dmsgpty.Whitelist entries the survey list doesn't), so
			// we re-check rather than reuse `wl`.
			if api.ptyHandler != nil && api.ptyWhitelist != nil {
				remoteHost, _, err := net.SplitHostPort(req.RemoteAddr)
				if err == nil && ptyPKAllowed(api.ptyWhitelist, remoteHost) {
					links = append(links, `<a href="/pty">/pty</a> - web terminal (dmsgpty)`)
				}
			}

			// Related nodes — shown ONLY to authenticated callers (this
			// section is gated strictly on `wl`). As LINKS to each peer's
			// landing page over a resolving proxy (http://<pk>.dmsg).
			if api.relatedNodesProvider != nil {
				// Part 2: visors this node manages as a hypervisor.
				if managed := api.relatedNodesProvider.ManagedVisors(); len(managed) > 0 {
					links = append(links, "")
					links = append(links, "managed visors (this hypervisor):")
					for _, pk := range managed {
						links = append(links, fmt.Sprintf(`  <a href="http://%s.dmsg">%s.dmsg</a>`, pk.Hex(), pk.Hex()))
					}
				}
				// Part 3: hypervisors this visor is configured to trust.
				if hvs := api.relatedNodesProvider.ConfiguredHypervisors(); len(hvs) > 0 {
					links = append(links, "")
					links = append(links, "configured hypervisors:")
					for _, pk := range hvs {
						links = append(links, fmt.Sprintf(`  <a href="http://%s.dmsg">%s.dmsg</a>`, pk.Hex(), pk.Hex()))
					}
					links = append(links, `  <small>(an hv link resolves only through a proxy that can route to it)</small>`)
				}
			}
		}
		// Add forwarded ports visible on the landing page.
		// Use the request Host to construct proper URLs that work
		// in the browser (e.g. http://pk.skynet:8000/).
		if api.forwardedPortLister != nil {
			host := req.Host
			// Strip existing port from host if present
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			// If host is a bare PK (66 hex chars or 53 base32 chars),
			// append .skynet so generated URLs route through the
			// SOCKS5 proxy. Base32 is the canonical form (the only
			// one TLS-MITM can mint a cert for); hex is accepted
			// for backcompat with older URLs.
			if !strings.Contains(host, ".") && (len(host) == 66 || len(host) == 53) {
				host += ".skynet"
			}
			for _, fp := range api.forwardedPortLister.LandingPageEntries() {
				label := fp.Label
				if label == "" {
					label = fmt.Sprintf("port %d", fp.Port)
				}
				desc := ""
				if fp.Description != "" {
					desc = " - " + fp.Description
				}
				url := html.EscapeString(fmt.Sprintf("http://%s:%d/", host, fp.Port))
				links = append(links, fmt.Sprintf(`<a href="%s">%s</a>%s`, url, label, desc))
			}
		}

		// Render via the shared landingpage package so this page and the browser
		// wasm-visor's (cmd/wasm-visor) are the same frame — PK identity header
		// included. The links differ (this visor exposes more, gated on the
		// whitelist above); the styling/structure is shared.
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, landingpage.Render(api.publicKey, links)) //nolint:errcheck,gosec // the request Host is escaped above
	})

	// Catch-all: if a custom website handler is set, serve unmatched
	// routes through it. Visor endpoints always take priority since
	// they're registered as explicit routes above.
	//
	// Access control tiers on port 80:
	//   /health, /ping, /services — open to everyone
	//   /node-info, /visor.log, /debug/pprof — survey whitelist
	//   everything else (website) — forwarded port whitelist (if set)
	r.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if api.websiteHandler != nil {
			// Enforce the forwarded port's PK whitelist on the website.
			if api.forwardedPortLister != nil {
				if wl := api.forwardedPortLister.PortWhitelist(80); len(wl) > 0 {
					remotePK, _, err := net.SplitHostPort(req.RemoteAddr)
					if err != nil {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					allowed := false
					for _, pk := range wl {
						if remotePK == pk.String() {
							allowed = true
							break
						}
					}
					if !allowed {
						w.WriteHeader(http.StatusForbidden)
						return
					}
				}
			}
			api.websiteHandler.ServeHTTP(w, req)
			return
		}
		writeString(w, http.StatusNotFound, "404 not found")
	})

	var h http.Handler = r
	if printLog {
		h = loggingMiddleware(h)
	}
	api.Handler = recoverPanics(log, h)
	return api
}

// SetWebsiteHandler sets a custom HTTP handler for serving unmatched
// routes on the visor's DMSG/skynet port 80. Visor system endpoints
// (/health, /node-info, /services, etc.) always take priority.
//
// Use cases:
// - Static file server: http.FileServer(http.Dir("/path/to/site"))
// - Reverse proxy to a local web app: httputil.ReverseProxy
// - The reward system UI handler
func (api *API) SetWebsiteHandler(h http.Handler) {
	api.websiteHandler = h
}

// SetStatusPage serves h at /status and links it from the landing page.
func (api *API) SetStatusPage(h http.Handler) {
	api.statusPage = h
}

func (api *API) health(w http.ResponseWriter, req *http.Request) {
	// /health carries the dmsg_address only — the public_key is redundant
	// (it's the host part of dmsg_address) and is shown on the landing page
	// instead. PublicKey is left unset; it's omitempty so it drops from the
	// JSON entirely.
	resp := httputil.HealthCheckResponse{
		ServiceName: "visor",
		BuildInfo:   buildinfo.Get(),
		StartedAt:   api.startedAt,
		DmsgAddr:    api.dmsgAddress,
	}

	// Add transport stats if provider is available
	if api.healthStatsProvider != nil {
		resp.PublicAutoconnect = api.healthStatsProvider.IsPublicAutoconnectRunning()
		resp.StcprCount, resp.SudphCount = api.healthStatsProvider.GetTransportCounts()
		resp.TransportCounts = api.healthStatsProvider.GetTransportTypeCounts()
		resp.NetworkTypes = api.healthStatsProvider.GetNetworkTypes()
		if d, ok := api.healthStatsProvider.(dmsgHealthProvider); ok {
			resp.DmsgSessions = d.DmsgSessionCount()
			resp.DmsgServer = d.DmsgServerHealth()
		}
	}

	jsonObject, err := json.Marshal(resp)
	if err != nil {
		httputil.GetLogger(req).WithError(err).Errorf("failed to encode json response")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	_, err = w.Write(jsonObject)
	if err != nil {
		httputil.GetLogger(req).WithError(err).Errorf("failed to write json response")
	}
}

// SetHealthStatsProvider sets the health stats provider after initialization.
func (api *API) SetHealthStatsProvider(provider HealthStatsProvider) {
	api.healthStatsProvider = provider
}

// SetIdentity records the visor's public key (hex) and dmsg listening
// address (`<pk>:<dmsg-port>`). The two are surfaced on different pages to
// avoid redundancy: the landing page names itself by public key, while the
// /health response carries the dmsg address (whose host part is the same
// key). Called from visor init.
func (api *API) SetIdentity(publicKey, dmsgAddress string) {
	api.publicKey = publicKey
	api.dmsgAddress = dmsgAddress
}

// SetServiceLister sets the service catalog provider. Called from
// visor init after the ServiceRegistry is populated.
func (api *API) SetServiceLister(lister ServiceLister) {
	api.serviceLister = lister
}

// SetForwardedPortLister sets the forwarded port provider for the landing page.
func (api *API) SetForwardedPortLister(lister ForwardedPortLister) {
	api.forwardedPortLister = lister
}

// SetCXOFeedsLister sets the CXO feed catalog provider. Called from
// visor init after the user-feed registry is wired up.
// SetPtyHandler installs the dmsgpty UI handler under /pty, gated
// by the supplied whitelist. The whitelist is expected to mirror
// the dmsgpty Host's whitelist (configured PKs + hypervisor PKs +
// the visor's own PK), so the same set of peers that can connect
// directly to dmsgpty over dmsg can also reach the web terminal.
// Pass a nil/empty whitelist or nil handler to disable; the route
// stays registered and returns 404/403, which is the correct
// signal to a probing client.
func (api *API) SetPtyHandler(h http.Handler, whitelist pty.Whitelist) {
	api.ptyHandler = h
	api.ptyWhitelist = whitelist
}

func (api *API) SetCXOFeedsLister(lister CXOFeedsLister) {
	api.cxoFeedsLister = lister
}

// SetRelatedNodesProvider installs the provider whose managed-visors and
// configured-hypervisors lists are shown (as links) on the landing page to
// authenticated callers. Called from visor init.
func (api *API) SetRelatedNodesProvider(p RelatedNodesProvider) {
	api.relatedNodesProvider = p
}

func whitelistAuth(whitelistedPKs []cipher.PubKey, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Get the remote PK.
		remotePK, _, err := net.SplitHostPort(req.RemoteAddr)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Check if the remote PK is whitelisted.
		whitelisted := len(whitelistedPKs) == 0
		for _, whitelistedPK := range whitelistedPKs {
			if remotePK == whitelistedPK.String() {
				whitelisted = true
				break
			}
		}
		if !whitelisted {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// jsonObj is a small JSON object body, such as {"error": "..."}.
type jsonObj map[string]string

func writeJSON(w http.ResponseWriter, code int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(data) //nolint:errcheck
}

func writeString(w http.ResponseWriter, code int, format string, args ...any) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, format, args...) //nolint:errcheck
}

func flush(w http.ResponseWriter) {
	_ = http.NewResponseController(w).Flush() //nolint:errcheck
}

// recoverPanics answers 500 when a handler panics, as the gin router did.
func recoverPanics(log *logging.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler { //nolint:errorlint
					panic(v)
				}
				log.Errorf("panic serving %s: %v", req.URL.Path, v)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, req)
	})
}

// statusWriter records the response status for the request log. Unwrap lets
// http.ResponseController reach Flush and Hijack on the real writer.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Flush() { flush(s.ResponseWriter) }

func (s *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(s.ResponseWriter).Hijack()
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, req)
		latency := time.Since(start)
		if latency > time.Minute {
			latency = latency.Truncate(time.Second)
		}
		statusCode := sw.status
		if statusCode == 0 {
			statusCode = http.StatusOK
		}
		method := req.Method
		path := req.URL.Path
		// Get the background color based on the status code
		statusCodeBackgroundColor := getBackgroundColor(statusCode)
		// Get the method color
		methodColor := getMethodColor(method)
		// Print the logging in a custom format which includes the publickeyfrom req.RemoteAddr ex.:
		// [DMSGHTTP] 2023/05/18 - 19:43:15 | 200 |    10.80885ms |                 | 02b5ee5333aa6b7f5fc623b7d5f35f505cb7f974e98a70751cf41962f84c8c4637:49153 | GET      /node-info.json
		fmt.Printf("[DMSGHTTP] %s |%s %3d %s| %13v | %15s | %72s |%s %-7s %s %s\n",
			time.Now().Format("2006/01/02 - 15:04:05"),
			statusCodeBackgroundColor,
			statusCode,
			resetColor(),
			latency,
			clientIP(req),
			req.RemoteAddr,
			methodColor,
			method,
			resetColor(),
			path,
		)
	})
}

// clientIP is the request's remote IP, empty for a dmsg or skynet peer whose
// address is a public key.
func clientIP(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil || net.ParseIP(host) == nil {
		return ""
	}
	return host
}

func getBackgroundColor(statusCode int) string {
	switch {
	case statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices:
		return green
	case statusCode >= http.StatusMultipleChoices && statusCode < http.StatusBadRequest:
		return white
	case statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError:
		return yellow
	default:
		return red
	}
}

func getMethodColor(method string) string {
	switch method {
	case http.MethodGet:
		return blue
	case http.MethodPost:
		return cyan
	case http.MethodPut:
		return yellow
	case http.MethodDelete:
		return red
	case http.MethodPatch:
		return green
	case http.MethodHead:
		return magenta
	case http.MethodOptions:
		return white
	default:
		return reset
	}
}

func resetColor() string {
	return reset
}

const (
	green   = "\033[97;42m"
	white   = "\033[90;47m"
	yellow  = "\033[90;43m"
	red     = "\033[97;41m"
	blue    = "\033[97;44m"
	magenta = "\033[97;45m"
	cyan    = "\033[97;46m"
	reset   = "\033[0m"
)
