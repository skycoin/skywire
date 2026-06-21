//go:build !tinygo

// Package dmsgclient pkg/dmsg/dmsgclient/seeded_upgrade_native.go
package dmsgclient

import (
	"context"
	"net/http"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
)

// upgradeDiscovery (native) backs the registering-fallback discovery with an
// HTTP-over-dmsg client (net/http + dmsghttp transport over the client's own
// sessions). See seeded.go for the rationale; the TinyGo build supplies a
// net/http-free equivalent in seeded_upgrade_tinygo.go.
func upgradeDiscovery(ctx context.Context, log *logging.Logger, dmsgC *dmsg.Client, dClient disc.APIClient, discDmsgAddr string) error {
	dmsgHTTP := &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}
	httpDisc := disc.NewHTTP(discDmsgAddr, dmsgHTTP, log)
	dmsgC.SetDiscoveryClients([]disc.APIClient{NewRegisteringFallbackDiscClient(dClient, httpDisc, log)})
	return nil
}
