// Package logserver contains api's for the logserver
package logserver

import (
	"encoding/json"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// API register all the API endpoints.
// It implements a net/http.Handler.
type API struct {
	http.Handler

	logger    *logging.Logger
	startedAt time.Time
}

// New creates a new api.
func New(log *logging.Logger, tpLogPath, localPath, customPath string, whitelistedPKs []cipher.PubKey, printLog bool) *API {
	api := &API{
		logger:    log,
		startedAt: time.Now(),
	}

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	if printLog {
		r.Use(middleware.Logger)
		r.Use(middleware.Recoverer)
	}
	r.Use(httputil.SetLoggerMiddleware(log))

	r.Get("/health", api.health)

	fsTP := http.FileServer(http.Dir(tpLogPath))
	r.Handle("/*", http.StripPrefix("/", fsTP))

	fsLocal := http.FileServer(http.Dir(localPath))
	r.Handle("/"+skyenv.NodeInfo, http.StripPrefix("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		whitelistFSHandler(w, r, localPath, whitelistedPKs)
	})))

	r.Handle("/transport_logs/*", http.StripPrefix("/", fsLocal))

	fsCustom := http.FileServer(http.Dir(customPath))
	r.Handle("/*", http.StripPrefix("/", fsCustom))

	api.Handler = r
	return api
}

func (api *API) health(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Get()
	api.writeJSON(w, r, http.StatusOK, httputil.HealthCheckResponse{
		BuildInfo: info,
		StartedAt: api.startedAt,
	})
}

func (api *API) writeJSON(w http.ResponseWriter, r *http.Request, code int, object interface{}) {
	jsonObject, err := json.Marshal(object)
	if err != nil {
		api.log(r).WithError(err).Errorf("failed to encode json response")
		w.WriteHeader(http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	_, err = w.Write(jsonObject)
	if err != nil {
		api.log(r).WithError(err).Errorf("failed to write json response")
	}
}

func (api *API) log(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
}

func whitelistFSHandler(w http.ResponseWriter, r *http.Request, serveDir string, whitelistedPKs []cipher.PubKey) {
	start := time.Now()
	// Get the remote PK.
	remotePK, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	// Check if the remote PK is whitelisted.
	whitelisted := false
	if len(whitelistedPKs) == 0 {
		whitelisted = true
	} else {
		for _, whitelistedPK := range whitelistedPKs {
			if remotePK == whitelistedPK.String() {
				whitelisted = true
				break
			}
		}
	}
	// If the remote PK is whitelisted, serve the file.
	if whitelisted {
		filePath := serveDir + r.URL.Path
		file, err := os.Open(filePath) //nolint
		if err != nil {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}
		defer file.Close() //nolint

		_, filename := path.Split(filePath)
		w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(filename)))
		http.ServeContent(w, r, filename, time.Time{}, file)

		// Log the response status and time taken.
		elapsed := time.Since(start)
		log.Printf("[DMSGHTTP] %s %s | %d | %v | %s | %s %s\n", start.Format("2006/01/02 - 15:04:05"), r.RemoteAddr, http.StatusOK, elapsed, r.Method, r.Proto, r.URL)
		return
	}
	// Otherwise, return a 403 Forbidden error.
	http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
	// Log the response status and time taken.
	elapsed := time.Since(start)
	log.Printf("[DMSGHTTP] %s %s | %d | %v | %s | %s %s\n", start.Format("2006/01/02 - 15:04:05"), r.RemoteAddr, http.StatusForbidden, elapsed, r.Method, r.Proto, r.URL)
}
