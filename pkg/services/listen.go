// Package services pkg/services/listen.go c2-vis-appsvc
//
// The startup steps every discovery-type service shares: its dmsg identity,
// its HTTP and dmsghttp listeners, its charts page, and its run loop.
package services

import (
	"context"
	"fmt"
	"net/http"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/svcmode"
)

// Identity is the dmsg identity a service listens with.
type Identity struct {
	PK       cipher.PubKey
	SK       cipher.SecKey
	DmsgPort uint16
	// DmsgAddr is "<pk>:<port>", empty without a key.
	DmsgAddr string
}

// Identity returns the configured key pair, deriving the public key when only
// the secret key is set, and the dmsghttp port and address.
func (c *Common) Identity(log *logging.Logger) Identity {
	id := Identity{PK: c.PubKey, SK: c.SecKey, DmsgPort: c.DmsgPort}
	if id.PK.Null() && !id.SK.Null() {
		if pk, err := id.SK.PubKey(); err != nil {
			log.WithError(err).Warn("No SecKey found. Skipping serving on dmsghttp.")
		} else {
			id.PK = pk
		}
	}
	if id.DmsgPort == 0 {
		id.DmsgPort = dmsg.DefaultDmsgHTTPPort
	}
	if !id.PK.Null() {
		id.DmsgAddr = fmt.Sprintf("%s:%d", id.PK.Hex(), id.DmsgPort)
	}
	return id
}

// DmsgDiscovery is the dmsg discovery address of cfg, the deployment's when
// unset. Discovery is reached over dmsg only.
func DmsgDiscovery(cfg cmdutil.DmsgConfig) string {
	if cfg.DiscoveryDmsg != "" {
		return cfg.DiscoveryDmsg
	}
	return dmsg.DiscAddr()
}

// DmsgDiscEntries is the dmsg servers the service bootstraps from: the
// configured ones, or the deployment's when none are set.
func DmsgDiscEntries(servers []*disc.Entry) []disc.Entry {
	if len(servers) == 0 {
		return dmsg.Prod.DmsgServers
	}
	out := make([]disc.Entry, 0, len(servers))
	for _, e := range servers {
		if e != nil {
			out = append(out, *e)
		}
	}
	return out
}

// Listener describes the listeners Listen starts for one service.
type Listener struct {
	// Name prefixes errors, e.g. "address-resolver".
	Name string
	// DefaultAddr is the plain HTTP address when Common.Addr is empty.
	DefaultAddr string
	Handler     http.Handler
	Dmsg        cmdutil.DmsgConfig
	Stats       *charts.ServiceStats
	// OnDmsgServersUpdated receives the dmsg servers the service is on.
	OnDmsgServersUpdated func([]string)
}

// Listen starts the HTTP and dmsghttp listeners selected by Common.Mode.
func (c *Common) Listen(ctx context.Context, id Identity, l Listener, log *logging.Logger) (*svcmode.Handle, error) {
	mode, err := svcmode.ResolveMode(c.Mode, !id.SK.Null())
	if err != nil {
		return nil, fmt.Errorf("%s: invalid mode: %w", l.Name, err)
	}
	addr := c.Addr
	if addr == "" {
		addr = l.DefaultAddr
	}
	h, err := svcmode.Start(ctx, svcmode.Config{
		Stats:                l.Stats,
		Mode:                 mode,
		LogHookLevel:         c.LogHookLevel,
		HTTPAddr:             addr,
		Handler:              l.Handler,
		PK:                   id.PK,
		SK:                   id.SK,
		DmsgPort:             id.DmsgPort,
		DmsgDiscoveryDmsg:    DmsgDiscovery(l.Dmsg),
		DmsgServerType:       l.Dmsg.ServerType,
		EmbeddedDmsgServers:  DmsgDiscEntries(l.Dmsg.Servers),
		SurveyWhitelist:      c.SurveyKeys(),
		Log:                  log,
		OnDmsgServersUpdated: l.OnDmsgServersUpdated,
	})
	if err != nil {
		return nil, fmt.Errorf("%s: start listeners: %w", l.Name, err)
	}
	return h, nil
}

// ServeCharts serves page alone on addr until ctx ends. An empty addr does
// nothing.
func ServeCharts(ctx context.Context, addr string, page http.HandlerFunc, log *logging.Logger, extra ...charts.Extra) {
	if addr == "" {
		return
	}
	log.Infof("Serving the charts page on %s", addr)
	go func() {
		if err := charts.Serve(ctx, addr, page, extra...); err != nil {
			log.WithError(err).Error("charts listener failed")
		}
	}()
}

// Wait returns nil when ctx ends, or the error of the first listener to fail.
func Wait(ctx context.Context, h *svcmode.Handle, log *logging.Logger) error {
	select {
	case <-ctx.Done():
		return nil
	case err := <-h.Errors():
		log.WithError(err).Error("listener failed")
		return err
	}
}

// OpenChartsStore keeps chart samples in the service's redis under prefix, or
// in memory when the store is not redis or redis is unavailable.
func OpenChartsStore(sc storeconfig.Config, prefix string, log *logging.Logger) charts.Store {
	if sc.Type != storeconfig.Redis {
		return charts.NewMemoryStore()
	}
	st, err := charts.NewRedisStore(sc.URL, sc.Password, prefix)
	if err != nil {
		log.WithError(err).Warn("charts: redis unavailable, keeping samples in memory")
		return charts.NewMemoryStore()
	}
	return st
}
