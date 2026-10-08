// Package visor pkg/visor/transport_list.go c3-vis-core
package visor

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/transport"
)

// transportListTTL bounds how often the signed list is rebuilt for GET
// /transports. A burst of neighbor fetches shares one build.
const transportListTTL = 5 * time.Second

type transportListCache struct {
	mu      sync.Mutex
	at      time.Time
	body    []byte
	version string
}

// TransportListBody is this visor's own live transport list, signed by it.
// It implements logserver.TransportListProvider.
func (v *Visor) TransportListBody() ([]byte, string, error) {
	c := &v.tpList
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.body != nil && time.Since(c.at) < transportListTTL {
		return c.body, c.version, nil
	}
	if v.tpM == nil {
		return nil, "", errors.New("transport manager not running")
	}
	var entries []*transport.Entry
	v.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
		if tp == nil || tp.IsClosed() || tp.Entry.Label == transport.LabelSetup {
			return true
		}
		e := tp.Entry
		entries = append(entries, &e)
		return true
	})
	now := time.Now()
	l, err := transport.NewSignedList(v.conf.PK, v.conf.SK, now.Unix(), entries)
	if err != nil {
		return nil, "", err
	}
	body, err := json.Marshal(l)
	if err != nil {
		return nil, "", err
	}
	c.at, c.body, c.version = now, body, l.Version()
	return c.body, c.version, nil
}
