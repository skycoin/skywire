package dmsghttp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/disc"

	"github.com/skycoin/skycoin/src/util/logging"
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
		Info("Serving...")

	return http.Serve(lis, a)
}
