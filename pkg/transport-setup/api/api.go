// Package api pkg/transport-setup/api/api.go
package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-playground/validator/v10"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
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

	// Single dmsg client with self-hosted dmsg-discovery — matches the
	// visor's post-#3136 bootstrap (pkg/visor/init_dmsg.go + pkg/dmsgc/
	// dmsgc.go). Plain-HTTP discovery (conf.Dmsg.Discovery) is no longer
	// supported for deployment services; conf.Dmsg.DiscoveryDmsg is
	// required.
	//
	// The seed-server set is conf.Dmsg.Servers ∪ dmsg.Prod.DmsgServers,
	// deduped by Static PK with operator-supplied entries first. A
	// deployment may therefore omit conf.Dmsg.Servers and still
	// bootstrap — the embedded Prod set is the default. Same merge
	// shape as dmsgsrv.buildTransitDmsg (pkg/services/dmsgsrv/
	// dmsgsrv.go) and the RSN (pkg/router/setupnode.go).
	//
	// Pattern:
	//   - direct.Client preloaded with the local PK + dmsg-disc PK,
	//     synthesizing client entries that delegate to every seed
	//     server. Reads (Entry/AvailableServers) short-circuit through
	//     this for service PKs and configured servers — no HTTP-over-
	//     dmsg round-trip to the entry-less, root-of-trust dmsg-disc.
	//   - disc.NewHTTP against the dmsg:// URL with an empty http.Client.
	//   - NewRegisteringFallbackDiscClient over (direct, http): READ
	//     direct-first, WRITE via dmsg-HTTP so the service's own entry
	//     IS published to the real dmsg-discovery.
	//   - SINGLE dmsg.Client built against the wrapped disc.
	//   - SeedEntryCache for every seed server (bootstrap dial path
	//     + the Serve-loop fallback in #3146).
	//   - httpC.Transport wired to dmsghttp.MakeHTTPTransport(dmsgC)
	//     AFTER construction — the transport isn't invoked until the
	//     first PostEntry/Entry call, by which time Serve has already
	//     established sessions to the seeded servers. Same ordering the
	//     visor relies on.
	httpC := &http.Client{}
	dmsgDisc := disc.NewHTTP(conf.Dmsg.DiscoveryDmsg, httpC, log)

	servers := make([]*disc.Entry, 0, len(conf.Dmsg.Servers)+len(dmsg.Prod.DmsgServers))
	seenPK := map[cipher.PubKey]struct{}{}
	addServer := func(e *disc.Entry) {
		if e == nil || e.Static.Null() || e.Server == nil {
			return
		}
		if _, ok := seenPK[e.Static]; ok {
			return
		}
		seenPK[e.Static] = struct{}{}
		servers = append(servers, e)
	}
	for _, e := range conf.Dmsg.Servers {
		addServer(e)
	}
	for i := range dmsg.Prod.DmsgServers {
		addServer(&dmsg.Prod.DmsgServers[i])
	}

	seedKeys := append(cipher.PubKeys{conf.PK}, dmsgServicePKs(conf.Dmsg.DiscoveryDmsg)...)
	entries := direct.GetAllEntries(seedKeys, servers)
	directDisc := direct.NewClient(entries, log)
	discClient := dmsgclient.NewRegisteringFallbackDiscClient(directDisc, dmsgDisc, log)

	client := dmsg.NewClient(conf.PK, conf.SK, discClient, dmsgConf)
	for _, srv := range servers {
		client.SeedEntryCache(srv.Static, srv)
	}
	httpC.Transport = dmsghttp.MakeHTTPTransport(context.Background(), client)

	return client
}

// dmsgServicePKs extracts the public key from a dmsg:// service URL
// (e.g. "dmsg://<pk>:<port>") so the dmsg-discovery PK can be added to
// the static seed-client mapping. Returns an empty slice for blank or
// malformed URLs — the caller is then bootstrapped with only its own
// PK in the synthetic seed map, which is the same shape as a brand-new
// service with no DiscoveryDmsg configured.
func dmsgServicePKs(discoveryDmsg string) cipher.PubKeys {
	if discoveryDmsg == "" {
		return nil
	}
	const prefix = "dmsg://"
	if len(discoveryDmsg) <= len(prefix) || discoveryDmsg[:len(prefix)] != prefix {
		return nil
	}
	var addr dmsg.Addr
	if err := addr.Set(discoveryDmsg[len(prefix):]); err != nil {
		return nil
	}
	return cipher.PubKeys{addr.PK}
}
