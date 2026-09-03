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

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	tpdapi "github.com/skycoin/skywire/pkg/deployment/tpd/api"
	"github.com/skycoin/skywire/pkg/transport"
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
	// The CXO publisher gzips the snapshot; return decompressed JSON to callers
	// (raw bodies from an older publisher pass through unchanged).
	return restoreTransportIDs(cxoutils.Gunzip(body)), nil
}

// restoreTransportIDs reconstitutes the derivable t_id the current publisher
// drops from each all-transports entry (see allTransportsWireEntry in the TPD
// publisher). It unmarshals the entries, recomputes any zero t_id from
// (edges, type) via transport.MakeTransportID, and re-marshals — so downstream
// consumers (`tp tree`, `tp viz`) see complete entries just like the HTTP
// /all-transports body. Dual-parse: an entry that already carries a non-zero
// t_id (older publisher) is left untouched. On any decode error the original
// bytes are returned verbatim so a wire-shape we don't recognize still flows.
func restoreTransportIDs(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	var entries []*transport.Entry
	if err := json.Unmarshal(body, &entries); err != nil {
		return body
	}
	changed := false
	for _, e := range entries {
		if e == nil || e.ID != (uuid.UUID{}) {
			continue
		}
		e.ID = transport.MakeTransportID(e.Edges[0], e.Edges[1], e.Type)
		changed = true
	}
	if !changed {
		return body
	}
	out, err := json.Marshal(entries)
	if err != nil {
		return body
	}
	return out
}
