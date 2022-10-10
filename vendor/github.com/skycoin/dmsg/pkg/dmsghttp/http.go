// Package dmsghttp pkg/dmsghttp/http.go
package dmsghttp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"

	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
)

// ListenAndServe serves http over dmsg
func ListenAndServe(ctx context.Context, pk cipher.PubKey, sk cipher.SecKey, a http.Handler, dClient disc.APIClient, dmsgPort uint16,
	config *dmsg.Config, dmsgC *dmsg.Client, log *logging.Logger) error {

	lis, err := dmsgC.Listen(dmsgPort)
	if err != nil {
		log.WithError(err).Fatal()
	}
	go func() {
		<-ctx.Done()
		if err := lis.Close(); err != nil {
			log.WithError(err).Error()
		}
	}()

	log.WithField("dmsg_addr", fmt.Sprintf("dmsg://%v", lis.Addr().String())).
		Debug("Serving...")
	srv := &http.Server{
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
		IdleTimeout:       30 * time.Second,
		ReadHeaderTimeout: 3 * time.Second,
		Handler:           a,
	}

	return srv.Serve(lis)
}
