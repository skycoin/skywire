// Package visor pkg/visor/api_all_transports.go c3-vis-core
//
// Reader-side helper for the TPD all-transports CXO snapshot. The
// publisher lives in pkg/deployment/tpd/api/cxo_all_transports_publisher.go
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
	"encoding/json"
	"errors"
)

// ErrTPDAllTransportsNotReady is returned when the CXO subscriber
// has nothing for the requested path yet (no manager, no PK, hasn't
// synced).
var ErrTPDAllTransportsNotReady = errors.New("tpd all-transports: cxo cache miss")

// FetchAllTransportsCXO returns the network's transport list as JSON, from
// TPD's routing feed — the same list route calculation uses. withSelf is
// kept for the callers that pass it: self-loops are not transports anyone
// routes over, and neither feed variant carries them any more. Caller
// treats the not-ready error as a cache miss and falls through to HTTP.
func (v *Visor) FetchAllTransportsCXO(_ bool) ([]byte, error) {
	mgr := v.CXOSubMgr()
	if mgr == nil {
		return nil, ErrTPDAllTransportsNotReady
	}
	mgr.AcquireFor(TabCLITransports)
	defer mgr.ReleaseFor(TabCLITransports)

	entries, ok := routingTransports(mgr)
	if !ok {
		return nil, ErrTPDAllTransportsNotReady
	}
	return json.Marshal(entries)
}
