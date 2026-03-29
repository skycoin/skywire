// Package dmsghttp pkg/dmsghttp/http.go
package dmsghttp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"

	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
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
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
		IdleTimeout:       30 * time.Second,
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
