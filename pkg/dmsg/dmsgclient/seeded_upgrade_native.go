//go:build !tinygo

// Package dmsgclient pkg/dmsg/dmsgclient/seeded_upgrade_native.go c1-net-dmsg
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
// net/http-free equivalent (and the seededDiscClient registration fix) in
// seeded_upgrade_tinygo.go. seedServers is unused here — the native path keeps
// the proven RegisteringFallbackDiscClient to avoid changing native standalone
// tools; it can adopt the seededDiscClient later.
func upgradeDiscovery(ctx context.Context, log *logging.Logger, dmsgC *dmsg.Client, dClient disc.APIClient, seedServers []*disc.Entry, discDmsgAddr string) error {
	_ = seedServers
	dmsgHTTP := &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}
	httpDisc := disc.NewHTTP(discDmsgAddr, dmsgHTTP, log)
	dmsgC.SetDiscoveryClients([]disc.APIClient{NewRegisteringFallbackDiscClient(dClient, httpDisc, log)})
	// Carrier convergence needs a LIVE lookup (fresh AddressWT/CertHashWT) that
	// bypasses the static-seed-first fallback above, which shadows those.
	dmsgC.SetLiveDiscovery(httpDisc)
	return nil
}
