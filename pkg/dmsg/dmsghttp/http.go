// Package dmsghttp pkg/dmsghttp/http.go
package dmsghttp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// ListenAndServe serves http over dmsg
func ListenAndServe(ctx context.Context, _ cipher.SecKey, a http.Handler, _ disc.APIClient, dmsgPort uint16,
	dmsgC *dmsg.Client, log *logging.Logger) error {

	lis, err := dmsgC.Listen(dmsgPort)
	if err != nil {
		return fmt.Errorf("dmsg listen on port %d: %w", dmsgPort, err)
	}

	log.WithField("dmsg_addr", fmt.Sprintf("dmsg://%v", lis.Addr().String())).
		Debug("Serving...")
	srv := &http.Server{
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		// IdleTimeout governs how long a kept-alive dmsg stream is held open
		// between requests. It MUST exceed the client's discovery refresh
		// cadence (dmsg.DefaultUpdateInterval = 60s entry re-POST, plus
		// dial-path entry lookups) or the server tears the stream down before
		// the client reuses it — defeating the client-side stream pool
		// (dmsghttp.HTTPTransport, 90s pool) and forcing a fresh noise-KK
		// handshake on EVERY periodic call. That per-request handshake storm
		// (secp256k1 ECDH in ClientSession.handshakeResponder) is exactly what
		// pinned dmsg-discovery at 100% CPU: a profile showed ~40% cum in the
		// handshake responder while the actual request rate was modest.
		//
		// Set just below dmsg.StreamIdleTimeout (120s) so the HTTP server
		// always closes the idle stream cleanly before the underlying stream's
		// read deadline fires. Was 30s — below the 60s refresh interval, so
		// the pool could never span two refreshes.
		IdleTimeout:       dmsg.StreamIdleTimeout - 5*time.Second,
		ReadHeaderTimeout: 3 * time.Second,
		MaxHeaderBytes:    1 << 14, // 16KB
		Handler:           a,
	}

	done := make(chan struct{})
	go func() { //nolint:gosec
		select {
		case <-ctx.Done():
			if err := srv.Shutdown(context.Background()); err != nil {
				log.WithError(err).Error()
			}
		case <-done:
		}
	}()

	err = srv.Serve(lis)
	close(done)
	return err
}
