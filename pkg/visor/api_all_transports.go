// Package visor pkg/visor/api_all_transports.go
//
// Reader-side helper for the TPD all-transports CXO snapshot. The
// publisher lives in pkg/transport-discovery/api/cxo_all_transports_publisher.go
// and writes JSON-encoded []*transport.Entry to two paths
// (transports/all/with-self, transports/all/without-self). This
// helper Acquires the TabCLITransports tab on the
// CXOSubscriptionManager, reads the requested path's snapshot, and
// returns the raw JSON body so FetchCXO can serve it to the CLI
// unchanged.
//
// "Acquire on each call" is intentional and lightweight — the
// manager's reference counting plus close-grace amortize repeated
// CLI invocations within ~10s, and a CLI not running once a minute
// keeps the cycle from running forever.
package visor

import (
	"errors"

	tpdapi "github.com/skycoin/skywire/pkg/transport-discovery/api"
)

// ErrTPDAllTransportsNotReady is returned when the CXO subscriber
// has nothing for the requested path yet (no manager, no PK, hasn't
// synced).
var ErrTPDAllTransportsNotReady = errors.New("tpd all-transports: cxo cache miss")

// FetchAllTransportsCXO returns the cached JSON for the requested
// variant (withSelf true → transports/all/with-self, else
// transports/all/without-self). Caller treats the not-ready error
// as a cache miss and falls through to HTTP.
func (v *Visor) FetchAllTransportsCXO(withSelf bool) ([]byte, error) {
	mgr := v.CXOSubMgr()
	if mgr == nil {
		return nil, ErrTPDAllTransportsNotReady
	}
	mgr.AcquireFor(TabCLITransports)
	defer mgr.ReleaseFor(TabCLITransports)

	path := tpdapi.AllTransportsPathWithoutSelf
	if withSelf {
		path = tpdapi.AllTransportsPathWithSelf
	}
	body, _, ok := mgr.Get(FeedTPDAllTransports, path)
	if !ok || len(body) == 0 {
		return nil, ErrTPDAllTransportsNotReady
	}
	return body, nil
}
