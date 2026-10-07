// Package visor pkg/visor/api_tpd_perkey_subscriber.go c3-vis-core
//
// Reader-side helper for TPD's per-key feed (perkey/stats): every visor's
// transports counted by type. The publisher lives in
// pkg/deployment/tpd/api/cxo_perkey_publisher.go. Served in the exact shape
// of GET /all-transports/per-key-stats, so a caller of that endpoint takes
// the feed in its place without knowing which one answered.
package visor

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	tpdapi "github.com/skycoin/skywire/pkg/deployment/tpd/api"
)

// ErrTPDPerKeyNotReady is returned when the per-key feed has nothing fresh
// to serve. Callers fall through to HTTP.
var ErrTPDPerKeyNotReady = errors.New("tpd per-key: cxo cache miss")

// perKeyMaxAge bounds how old the leaf may be. The publisher writes every
// minute, but holds a complete sample through up to five minutes of partial
// ones after a TPD restart; past both, the leaf is a stopped feed.
const perKeyMaxAge = 10 * time.Minute

// FetchTPDPerKeyCXO returns the /all-transports/per-key-stats body from the
// per-key feed and the time of the leaf it came from, or ErrTPDPerKeyNotReady.
func (v *Visor) FetchTPDPerKeyCXO() ([]byte, time.Time, error) {
	mgr := v.CXOSubMgr()
	if mgr == nil {
		return nil, time.Time{}, ErrTPDPerKeyNotReady
	}
	mgr.AcquireFor(TabNetworkView)
	defer mgr.ReleaseFor(TabNetworkView)

	if body, ts, ok := readPerKeyLeaf(mgr); ok && time.Since(ts) <= perKeyMaxAge {
		return body, ts, nil
	}
	// Cold or stale: AcquireFor only started the cycle, so wait briefly for
	// a sync rather than send the caller to HTTP on the first call.
	ctx, cancel := context.WithTimeout(context.Background(), feedFirstSyncTimeout(FeedTPDPerKey))
	_, _ = mgr.RefreshNow(ctx, FeedTPDPerKey) //nolint:errcheck
	cancel()
	if body, ts, ok := readPerKeyLeaf(mgr); ok && time.Since(ts) <= perKeyMaxAge {
		return body, ts, nil
	}
	return nil, time.Time{}, ErrTPDPerKeyNotReady
}

// readPerKeyLeaf decodes the published PerKeyStats and re-encodes its keys,
// the HTTP body. An empty table is a miss: the endpoint answers 404 then.
func readPerKeyLeaf(mgr statsSnapshot) ([]byte, time.Time, bool) {
	raw, ts, ok := mgr.Get(FeedTPDPerKey, tpdapi.PerKeyPath)
	if !ok || len(raw) == 0 {
		return nil, time.Time{}, false
	}
	var st tpdapi.PerKeyStats
	if err := json.Unmarshal(cxoutils.Gunzip(raw), &st); err != nil || len(st.Keys) == 0 {
		return nil, time.Time{}, false
	}
	body, err := json.Marshal(st.Keys)
	if err != nil {
		return nil, time.Time{}, false
	}
	return body, ts, true
}
