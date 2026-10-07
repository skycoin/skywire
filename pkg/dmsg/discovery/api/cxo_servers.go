// Package api pkg/dmsg/discovery/api/cxo_servers.go
//
// Registered dmsg servers on the clients-by-server feed, so visors keep their
// server cache from the snapshot they already hold instead of asking for
// all_servers over HTTP every few minutes.
//
// Each server is one leaf at clients-by-server/servers/<server-pk>: its signed
// discovery entry, version-framed and gzipped. The path sits under the feed's
// prefix because subscribers fetch only that prefix. Older readers ignore it:
// they take clients-by-server/<pk> as a batch and a deeper path only when it
// ends in /entry.
//
// A server re-signs its entry on every refresh and its free session count
// moves with every client, so a leaf is rewritten only when something a
// client dials by changes: its addresses, type, version or protocol. The
// signed body stays valid; its timestamp and session count are as of that
// change.
package api

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/discovery/serverfeed"
)

const serversCXOEvery = time.Minute

func serverLeafPath(pk cipher.PubKey) string { return serverfeed.LeafPath(pk) }

// serverKey is what a leaf is rewritten for: the entry without its signature,
// sequence, timestamp and free session count.
func serverKey(e *disc.Entry) string {
	c := *e
	c.Sequence, c.Timestamp, c.Signature = 0, 0, ""
	if e.Server != nil {
		s := *e.Server
		s.AvailableSessions = 0
		c.Server = &s
	}
	b, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return string(b)
}

type serverLeaf struct {
	pk   cipher.PubKey
	key  string
	body []byte
}

// PublishServers makes the server leaves match servers: a new or materially
// changed server is written and one no longer present is deleted.
func (p *ClientsByServerCXOPublisher) PublishServers(servers []*disc.Entry) {
	if p == nil || p.pub == nil {
		return
	}
	leaves := make([]serverLeaf, 0, len(servers))
	for _, e := range servers {
		if e == nil || e.Server == nil {
			continue
		}
		body, err := serverfeed.Encode(e)
		if err != nil {
			continue
		}
		leaves = append(leaves, serverLeaf{pk: e.Static, key: serverKey(e), body: body})
	}
	p.submit(func() {
		seen := make(map[cipher.PubKey]struct{}, len(leaves))
		for _, l := range leaves {
			seen[l.pk] = struct{}{}
			if p.servers[l.pk] == l.key {
				continue
			}
			if err := p.pub.Put(serverLeafPath(l.pk), l.body); err != nil {
				p.recordError(err)
				continue
			}
			p.servers[l.pk] = l.key
		}
		for pk := range p.servers {
			if _, ok := seen[pk]; ok {
				continue
			}
			if err := p.pub.Delete(serverLeafPath(pk)); err == nil {
				delete(p.servers, pk)
			}
		}
	})
}

// RunServersCXO publishes the registered servers to the feed every minute
// until ctx ends. A server whose entry expires drops out on the next round.
func (a *API) RunServersCXO(ctx context.Context, log logrus.FieldLogger) {
	t := time.NewTicker(serversCXOEvery)
	defer t.Stop()
	for {
		if p := a.cxoPublisher; p != nil {
			if servers, err := a.db.AllServers(ctx); err == nil {
				p.PublishServers(servers)
			} else {
				log.WithError(err).Debug("servers CXO: could not read servers")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
