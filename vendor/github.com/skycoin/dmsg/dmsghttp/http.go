package dmsghttp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/direct"

	"github.com/skycoin/skycoin/src/util/logging"
)

// ListenAndServe serves http over dmsg
func ListenAndServe(ctx context.Context, pk cipher.PubKey, sk cipher.SecKey, a http.Handler, dClient direct.APIClient, dmsgPort uint16,
	config *dmsg.Config, log *logging.Logger) error {
	dmsgC, closeDmsg, err := direct.StartDmsg(ctx, log, pk, sk, dClient, config)
	if err != nil {
		return fmt.Errorf("failed to start dmsg: %w", err)
	}
	defer closeDmsg()

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
