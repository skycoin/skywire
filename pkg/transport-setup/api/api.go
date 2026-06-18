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

	// Pick the dmsg-discovery URL and the http.Client used to query it.
	// Mirrors pkg/router/setupnode.go and dmsgclient.StartDmsgSelfHostedDisc:
	//
	//   - If conf.Dmsg.Discovery (plain HTTP) is set, use it as before.
	//   - Else if conf.Dmsg.DiscoveryDmsg + seed servers are set, fall
	//     into the self-hosted disc pattern: a registering fallback
	//     disc client whose READ path resolves seeded servers directly
	//     (no round-trip) and whose WRITE path (PutEntry — so this
	//     service's own entry IS published to the real dmsg-discovery)
	//     goes through an http.Client whose Transport is dmsghttp,
	//     routed over the same dmsg.Client we are building. Self-
	//     hosted: one client both registers itself and resolves
	//     bootstrap PKs statically, no plain-HTTP fallback.
	//   - Else: the original NewHTTP("") path, which returns a no-op
	//     discovery client; the Serve-loop fallback (#3146) still
	//     pulls seeded servers from SeedEntryCache for the initial
	//     dial, but the service can't publish its own entry — useful
	//     only for air-gapped / single-host setups.
	discURL := conf.Dmsg.Discovery
	httpC := &http.Client{}
	useSelfHostedDisc := discURL == "" && conf.Dmsg.DiscoveryDmsg != "" && len(conf.Dmsg.Servers) > 0
	if useSelfHostedDisc {
		discURL = conf.Dmsg.DiscoveryDmsg
	}

	dmsgDisc := disc.NewHTTP(discURL, httpC, log)
	discClient := dmsgDisc
	if useSelfHostedDisc {
		seedKeys := append(cipher.PubKeys{conf.PK}, dmsgServicePKs(conf.Dmsg.DiscoveryDmsg)...)
		entries := direct.GetAllEntries(seedKeys, conf.Dmsg.Servers)
		directDisc := direct.NewClient(entries, log)
		discClient = dmsgclient.NewRegisteringFallbackDiscClient(directDisc, dmsgDisc, log)
	}

	client := dmsg.NewClient(conf.PK, conf.SK, discClient, dmsgConf)

	// Pre-seed the entry cache with the configured dmsg servers so the
	// Serve loop's seeded-fallback path (#3146) has something to fall
	// back to during a fleet cold-start where live discovery briefly
	// has no entries. Also bootstraps the no-disc-URL fallback path.
	for _, srv := range conf.Dmsg.Servers {
		if srv == nil || srv.Static.Null() || srv.Server == nil {
			continue
		}
		client.SeedEntryCache(srv.Static, srv)
	}

	if useSelfHostedDisc {
		// Wire the dmsg.Client as its OWN http.Transport so disc.NewHTTP
		// reaches dmsg-discovery via dmsg-HTTP. Safe to set after
		// NewClient: the transport isn't dialed until the first PostEntry
		// /Entry call, by which time Serve has already established
		// sessions to the seeded servers. Pattern matches
		// dmsgclient.StartDmsgSelfHostedDisc.
		httpC.Transport = dmsghttp.MakeHTTPTransport(context.Background(), client)
	}

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
