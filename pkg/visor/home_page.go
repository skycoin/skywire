// Package visor pkg/visor/home_page.go c3-vis-core
//
// home.skynet (and home.dmsg) is the visor's directory of the mesh: the
// resolver's names and the visors the address resolver knows, each linked by
// its base32 name. "home" is an alias for the visor itself, so every path that
// reaches a mesh name (the resolving proxy, SkyDNS, a Wisp guest) reaches it
// the same way.
package visor

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsgweb"
)

// homeVisorsTTL is how long the address resolver's visor list is reused.
const homeVisorsTTL = 10 * time.Minute

// homeVisorCache holds the last visor list fetched for the directory.
type homeVisorCache struct {
	mu   sync.Mutex
	list []cipher.PubKey
	at   time.Time
}

// homePage renders the directory for suffix (".skynet" or ".dmsg") for this
// visor's own user.
func (v *Visor) homePage(suffix string) []byte { return v.homePageFor(suffix, true) }

// homePageFor renders the directory, with the visors this one manages only
// for its own user.
func (v *Visor) homePageFor(suffix string, ownUser bool) []byte {
	lists := &dmsgweb.HomeLists{Visors: v.meshVisors()}
	if ownUser {
		lists.Managed = v.ManagedVisors()
	}
	return dmsgweb.RenderHomePageWith(v.browseAliases(), suffix, v.conf.PK, lists)
}

// meshVisors returns the visors bound with the address resolver, sorted by
// name. A failed fetch keeps the last list.
func (v *Visor) meshVisors() []cipher.PubKey {
	c := &v.homeVisors
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.list != nil && time.Since(c.at) < homeVisorsTTL {
		return c.list
	}
	if v.arClient == nil {
		return c.list
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	bound, err := v.arClient.Transports(ctx)
	if err != nil {
		v.log.WithError(err).Debug("home page: address resolver visor list")
		return c.list
	}
	list := make([]cipher.PubKey, 0, len(bound))
	for pk := range bound {
		list = append(list, pk)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].DNSLabel() < list[j].DNSLabel() })
	c.list, c.at = list, time.Now()
	return list
}
