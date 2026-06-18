// Package api pkg/transport-setup/api/api.go
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-playground/validator/v10"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport-setup/config"
)

// API register all the API endpoints.
// It implements a net/http.Handler.
type API struct {
	http.Handler
	dmsgC     *dmsg.Client
	logger    *logging.Logger
	validator *validator.Validate
}

// New constructs a new API instance.
func New(log *logging.Logger, conf config.Config) *API {
	if log == nil {
		log = logging.NewMasterLogger().PackageLogger("transport_setup")
	}
	v := validator.New()
	api := &API{logger: log, validator: v}
	api.dmsgC = setupDmsgC(conf, log)

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP) //nolint:staticcheck
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(httputil.SetLoggerMiddleware(log))

	r.Post("/add", api.addTransport)
	r.Post("/remove", api.removeTransport)
	r.Get("/{pk}/transports", api.getTransports)
	api.Handler = r

	return api
}

func setupDmsgC(conf config.Config, log *logging.Logger) *dmsg.Client {
	dmsgConf := dmsg.DefaultConfig()
	disc := disc.NewHTTP(conf.Dmsg.Discovery, &http.Client{}, log)
	client := dmsg.NewClient(conf.PK, conf.SK, disc, dmsgConf)
	// Pre-seed the entry cache with the configured dmsg servers so the
	// Serve loop's seeded-fallback path (#3146) has something to fall
	// back to when conf.Dmsg.Discovery is empty (dmsg-only deployment,
	// disc.NewHTTP returns a no-op client) or during a fleet cold-start
	// where live discovery briefly has no entries. Without this, dmsg-
	// only TPS instances spin on "No entries found" indefinitely even
	// though tps.json explicitly lists upstream servers.
	for _, srv := range conf.Dmsg.Servers {
		if srv == nil || srv.Static.Null() || srv.Server == nil {
			continue
		}
		client.SeedEntryCache(srv.Static, srv)
	}
	return client
}
