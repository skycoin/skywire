// Package visor pkg/visor/init_embedded_services.go c3-vis-core
//
// Deployment services running inside the visor. Each block in
// config.embedded_services is built by its pkg/services factory and
// mounted under a path prefix on the dmsg HTTP port, next to the log
// server, so transport-discovery becomes dmsg://<visor pk>:80/tpd/...
// and is reached the way the visor itself is: one dmsg stream to this
// key, over any session that reaches it. The service keeps no key, no
// listener and no dmsg sessions of its own; its CXO aggregators and
// publishers run on the visor's dmsg client under the visor's key.
//
// Requests over dmsg carry the caller's key in RemoteAddr, which is
// what the services' whitelists and SW-Sig auth read, so their access
// rules are unchanged. The nonce path only exists for plain HTTP and
// is not served here.
package visor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"

	// The embeddable services register their factories at init.
	_ "github.com/skycoin/skywire/pkg/services/ar"
	_ "github.com/skycoin/skywire/pkg/services/rf"
	_ "github.com/skycoin/skywire/pkg/services/sd"
	_ "github.com/skycoin/skywire/pkg/services/tpd"
)

func initEmbeddedServices(ctx context.Context, v *Visor, log *logging.Logger) error {
	blocks := v.conf.EmbeddedServices
	if len(blocks) == 0 {
		return nil
	}
	if v.dmsgHTTPMux == nil {
		return fmt.Errorf("embedded services: dmsg HTTP mux not initialized")
	}
	host := services.Host{
		DmsgClient: v.dmsgC,
		PK:         v.conf.PK,
		SK:         v.conf.SK,
		DmsgAddr:   fmt.Sprintf("%s:%d", v.conf.PK.Hex(), visorconfig.DmsgHTTPPort),
	}
	seen := map[string]string{}
	for i, b := range blocks {
		factory, ok := services.Lookup(b.Type)
		if !ok {
			return fmt.Errorf("embedded services: block #%d (%s): unknown type %q (registered: %v)",
				i, b.Label(), b.Type, services.RegisteredTypes())
		}
		prefix := b.Prefix()
		if other, dup := seen[prefix]; dup {
			return fmt.Errorf("embedded services: block #%d (%s) and %s both mount at %s", i, b.Label(), other, prefix)
		}
		seen[prefix] = b.Label()

		svcLog := v.MasterLogger().PackageLogger(b.Label())
		svc, err := factory(b.Raw, svcLog)
		if err != nil {
			return fmt.Errorf("embedded services: block #%d (%s): build: %w", i, b.Label(), err)
		}
		emb, ok := svc.(services.Embeddable)
		if !ok {
			return fmt.Errorf("embedded services: block #%d (%s): type %q cannot run inside the visor", i, b.Label(), b.Type)
		}
		host.Log = svcLog
		handler, err := emb.Embed(ctx, host)
		if err != nil {
			return fmt.Errorf("embedded services: block #%d (%s): start: %w", i, b.Label(), err)
		}
		services.Mount(v.dmsgHTTPMux, prefix, handler)
		log.WithField("service", b.Label()).WithField("type", b.Type).
			WithField("addr", fmt.Sprintf("dmsg://%s%s", host.DmsgAddr, prefix)).
			Info("Embedded service mounted")

		// A block's plain-HTTP "addr" is honored too, for a host that is
		// still reached the old way during a transition: the same handler,
		// without the prefix, on that address.
		if addr := blockAddr(b); addr != "" {
			if err := servePlainHTTP(ctx, addr, handler, svcLog); err != nil {
				return fmt.Errorf("embedded services: block #%d (%s): listen on %s: %w", i, b.Label(), addr, err)
			}
		}
	}
	return nil
}

// blockAddr returns the block's plain-HTTP "addr", or "".
func blockAddr(b services.Block) string {
	var probe struct {
		Addr string `json:"addr,omitempty"`
	}
	_ = json.Unmarshal(b.Raw, &probe) //nolint:errcheck // Raw already parsed by the factory
	return probe.Addr
}

// servePlainHTTP serves handler on addr until ctx ends.
func servePlainHTTP(ctx context.Context, addr string, handler http.Handler, log *logging.Logger) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: skyenv.HTTPReadHeaderTimeout,
	}
	go func() {
		if err := srv.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.WithError(err).Warn("Embedded service plain-HTTP listener exited")
		}
	}()
	go func() { //nolint:gosec // G118: the shutdown runs after ctx has ended, so it cannot use it
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx) //nolint:errcheck
	}()
	log.WithField("addr", lis.Addr().String()).Info("Embedded service also served on plain HTTP")
	return nil
}
