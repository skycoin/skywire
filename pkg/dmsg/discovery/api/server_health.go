// Package api pkg/dmsg/discovery/api/server_health.go c2-dmsg-core
package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
)

// Sample keys for the load each registered dmsg server reports on /health.
const (
	chartSrvBytes   = "sh.bytes."
	chartSrvStreams = "sh.streams."
	chartSrvConns   = "sh.conn."
	chartSrvPeers   = "sh.peers."
	chartSrvActive  = "sh.active."
	chartSrvOK      = "sh.ok."
)

// serverHealthTimeout bounds one round of /health reads.
const serverHealthTimeout = 30 * time.Second

// serverHealth reads each registered dmsg server's /health over dmsg: the
// connections it holds, and the streams it relayed and the bytes they carried.
type serverHealth struct {
	client *http.Client
	mu     sync.Mutex
	prev   map[cipher.PubKey]httputil.DmsgServerHealth
}

// SetServerHealthClient lets the status page read each registered dmsg
// server's load from its /health, with c reaching them over dmsg.
func (a *API) SetServerHealthClient(c *http.Client) {
	a.srvHealth.Store(&serverHealth{client: c, prev: map[cipher.PubKey]httputil.DmsgServerHealth{}})
}

// collect adds each server's connections now, and what it relayed since the
// last call, to v. A server that does not answer, or runs a version without
// the figures, is marked with chartSrvOK 0.
func (h *serverHealth) collect(ctx context.Context, pks []cipher.PubKey, v map[string]float64) {
	ctx, cancel := context.WithTimeout(ctx, serverHealthTimeout)
	defer cancel()
	got := make([]*httputil.DmsgServerHealth, len(pks))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, pk := range pks {
		wg.Add(1)
		go func(i int, pk cipher.PubKey) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			got[i] = h.fetch(ctx, pk)
		}(i, pk)
	}
	wg.Wait()

	h.mu.Lock()
	defer h.mu.Unlock()
	for i, pk := range pks {
		k := pk.Hex()
		d := got[i]
		if d == nil {
			v[chartSrvOK+k] = 0
			continue
		}
		v[chartSrvOK+k] = 1
		v[chartSrvConns+k] = float64(d.ClientSessions)
		v[chartSrvPeers+k] = float64(d.PeerSessions)
		v[chartSrvActive+k] = float64(d.ActiveStreams)
		if p, ok := h.prev[pk]; ok {
			v[chartSrvStreams+k] = float64(since(d.StreamsRelayed, p.StreamsRelayed))
			v[chartSrvBytes+k] = float64(since(d.BytesUp, p.BytesUp) + since(d.BytesDown, p.BytesDown))
		}
		h.prev[pk] = *d
	}
}

// since is cur-prev, or cur when the counter restarted below prev.
func since(cur, prev uint64) uint64 {
	if cur < prev {
		return cur
	}
	return cur - prev
}

func (h *serverHealth) fetch(ctx context.Context, pk cipher.PubKey) *httputil.DmsgServerHealth {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s:80/health", pk.Hex()), nil)
	if err != nil {
		return nil
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var body httputil.HealthCheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil
	}
	return body.DmsgServer
}
