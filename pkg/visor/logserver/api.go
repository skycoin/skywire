// Package logserver contains api's for the logserver
package logserver

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
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
func New(log *logging.Logger, tpLogPath, localPath, customPath string, printLog bool) *API {
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
	r.Handle("/"+skyenv.SurveyFile, http.StripPrefix("/", fsLocal))

	r.Handle("/"+skyenv.RewardFile, http.StripPrefix("/", fsLocal))

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
