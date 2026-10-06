// Package visor pkg/visor/dmsg_servers_cxo.go
//
// The registered dmsg servers from dmsg discovery's clients-by-server feed,
// which the visor already holds for its entry lookups. The server cache
// refreshes from this snapshot and asks dmsg discovery over HTTP only when
// the feed carries no servers, as a publisher older than the server leaves
// does.
package visor

import (
	"encoding/json"
	"strings"

	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
)

// serverLeafPrefix and serverLeafVersion match dmsg discovery's publisher
// (pkg/dmsg/discovery/api/cxo_servers.go).
const (
	serverLeafPrefix  = clientsByServerPrefix + "servers/"
	serverLeafVersion = 1
)

// dmsgServersFromCXO returns the servers in the current snapshot, or nil
// when there is no snapshot yet or it has no server leaves.
func (v *Visor) dmsgServersFromCXO() []*dmsgdisc.Entry {
	mgr := v.CXOSubMgr()
	if mgr == nil {
		return nil
	}
	v.serversFeedOnce.Do(func() { mgr.AcquireFor(TabDmsgEntryLookup) })
	if mgr.LastSync(FeedDMSGDClientsByServer).IsZero() {
		return nil
	}
	var out []*dmsgdisc.Entry
	mgr.Walk(FeedDMSGDClientsByServer, serverLeafPrefix, func(path string, body []byte) bool {
		if strings.Contains(strings.TrimPrefix(path, serverLeafPrefix), "/") {
			return true
		}
		if e := decodeServerLeaf(body); e != nil {
			out = append(out, e)
		}
		return true
	})
	return out
}

func decodeServerLeaf(body []byte) *dmsgdisc.Entry {
	version, payload, ok := cxoutils.UnframeGzip(body)
	if !ok || version != serverLeafVersion {
		return nil
	}
	e := new(dmsgdisc.Entry)
	if err := json.Unmarshal(payload, e); err != nil || e.Server == nil {
		return nil
	}
	return e
}
