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
)

func initEmbeddedServices(ctx context.Context, v *Visor, log *logging.Logger) error {
	svcs := v.embeddedServices()
	if len(svcs) == 0 {
		return nil
	}
	host := services.Host{
		DmsgClient: v.dmsgC,
		PK:         v.conf.PK,
		SK:         v.conf.SK,
		DmsgAddr:   fmt.Sprintf("%s:%d", v.conf.PK.Hex(), visorconfig.DmsgHTTPPort),
		CXO:        visorCXOHost{v},
	}
	for _, es := range svcs {
		if es.err != nil {
			return fmt.Errorf("embedded services: %w", es.err)
		}
		if !es.ownPK.Null() {
			go v.runOwnKeyService(ctx, es)
			continue
		}
		if v.dmsgHTTPMux == nil {
			return fmt.Errorf("embedded services: dmsg HTTP mux not initialized")
		}
		host.Log = es.log
		handler, err := es.svc.Embed(ctx, host)
		if err != nil {
			es.mu.Lock()
			es.startErr = fmt.Errorf("start: %w", err)
			es.mu.Unlock()
			return fmt.Errorf("embedded services: %s: start: %w", es.label, err)
		}
		services.Mount(v.dmsgHTTPMux, es.prefix, handler)
		es.mu.Lock()
		es.running = true
		es.mu.Unlock()
		log.WithField("service", es.label).WithField("type", es.block.Type).
			WithField("addr", fmt.Sprintf("dmsg://%s%s", host.DmsgAddr, es.prefix)).
			Info("Embedded service mounted")

		// A block's plain-HTTP "addr" is honored too, for a host that is
		// still reached the old way during a transition: the same handler,
		// without the prefix, on that address.
		if addr := blockAddr(es.block); addr != "" {
			if err := servePlainHTTP(ctx, addr, handler, es.log); err != nil {
				return fmt.Errorf("embedded services: %s: listen on %s: %w", es.label, addr, err)
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

// Restart pacing for an own-key service that stopped on its own. Variables
// so tests can shorten them.
var (
	ownKeyMinBackoff = 5 * time.Second
	ownKeyMaxBackoff = 5 * time.Minute
	// ownKeySteadyRun resets the backoff: a run this long was not a crash loop.
	ownKeySteadyRun = 10 * time.Minute
)

// runOwnKeyService runs a service under its own key, as `svc run` would but
// inside the visor, until ctx ends. It is built again from its block after
// every stop, so a failure in one service never takes the visor down.
func (v *Visor) runOwnKeyService(ctx context.Context, es *embeddedService) {
	backoff := ownKeyMinBackoff
	for {
		start := time.Now()
		svc, err := es.factory(es.ownRaw, es.log)
		if err == nil {
			es.mu.Lock()
			es.running, es.startErr, es.current = true, nil, svc
			es.mu.Unlock()
			es.log.WithField("addr", "dmsg://"+es.ownPK.Hex()).Info("Embedded service started under its own key")
			err = svc.Run(ctx)
		}
		es.mu.Lock()
		es.running, es.current = false, nil
		if err != nil {
			es.startErr = err
		}
		es.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > ownKeySteadyRun {
			backoff = ownKeyMinBackoff
		}
		es.log.WithError(err).WithField("retry_in", backoff).Warn("Embedded service stopped; starting it again")
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		es.mu.Lock()
		es.restarts++
		es.mu.Unlock()
		backoff = min(backoff*2, ownKeyMaxBackoff)
	}
}
