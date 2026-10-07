// Package visor pkg/visor/dmsg_servers_cxo.go
//
// The registered dmsg servers from dmsg discovery's clients-by-server feed,
// which the visor already holds for its entry lookups. The server cache
// refreshes from this snapshot and asks dmsg discovery over HTTP only when
// the feed carries no servers, as a publisher older than the server leaves
// does.
package visor

import (
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/discovery/serverfeed"
)

// dmsgServersFromCXO returns the servers in the current snapshot, or nil
// when there is no snapshot yet or it has no server leaves.
func (v *Visor) dmsgServersFromCXO() []*dmsgdisc.Entry {
	mgr := v.CXOSubMgr()
	if mgr == nil {
		return nil
	}
	v.serversFeedOnce.Do(func() { mgr.AcquireFor(TabDmsgEntryLookup) })
	servers, _ := serverfeed.Servers(mgr)
	return servers
}
