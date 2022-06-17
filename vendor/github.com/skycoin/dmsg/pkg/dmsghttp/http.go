package dmsghttp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire-utilities/pkg/cipher"

	"github.com/skycoin/skywire-utilities/pkg/logging"
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

	return http.Serve(lis, a)
}
