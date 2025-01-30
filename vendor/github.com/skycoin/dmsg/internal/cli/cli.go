// Package cli internal/cli/cli.go
package cli

import (
	"context"
	"fmt"
	"net/http"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"

	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
)

// StartDmsg starts dmsg returns a dmsg client for the given dmsg discovery
func StartDmsg(ctx context.Context, dmsgLogger *logging.Logger, pk cipher.PubKey, sk cipher.SecKey, httpClient *http.Client, dmsgDisc string, dmsgSessions int) (dmsgC *dmsg.Client, stop func(), err error) {
	dmsgC = dmsg.NewClient(pk, sk, disc.NewHTTP(dmsgDisc, httpClient, dmsgLogger), &dmsg.Config{MinSessions: dmsgSessions})
	go dmsgC.Serve(context.Background())

	stop = func() {
		err := dmsgC.Close()
		dmsgLogger.WithError(err).Debug("Disconnected from dmsg network.")
		fmt.Printf("\n")
	}
	dmsgLogger.WithField("public_key", pk.String()).WithField("dmsg_disc", dmsgDisc).
		Debug("Connecting to dmsg network...")
	select {
	case <-ctx.Done():
		stop()
		return nil, nil, ctx.Err()

	case <-dmsgC.Ready():
		dmsgLogger.Debug("Dmsg network ready.")
		return dmsgC, stop, nil
	}
}

//TODO
/*
func startDmsgDirect(ctx context.Context, v *Visor, log *logging.Logger) error { //nolint:all
	var keys cipher.PubKeys
	servers := v.conf.Dmsg.Servers

	if len(servers) == 0 {
		return nil
	}

	keys = append(keys, v.conf.PK)
	entries := direct.GetAllEntries(keys, servers)
	dClient := direct.NewClient(entries, v.MasterLogger().PackageLogger("dmsg_http:direct_client"))

	dmsgDC, closeDmsgDC, err := direct.StartDmsg(ctx, v.MasterLogger().PackageLogger("dmsg_http:dmsgDC"),
		v.conf.PK, v.conf.SK, dClient, dmsg.DefaultConfig())
	if err != nil {
		return fmt.Errorf("failed to start dmsg: %w", err)
	}

	dmsgHTTP := http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgDC)}

	v.pushCloseStack("dmsg_http", func() error {
		closeDmsgDC()
		return nil
	})

	v.initLock.Lock()
	v.dClient = dClient
	v.dmsgHTTP = &dmsgHTTP
	v.dmsgDC = dmsgDC
	v.initLock.Unlock()
	time.Sleep(time.Duration(len(entries)) * time.Second)
	return nil
}
*/
