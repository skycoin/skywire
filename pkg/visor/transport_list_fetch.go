// Package visor pkg/visor/transport_list_fetch.go c3-vis-core
package visor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// A fetched list is used as is for listFreshFor, then re-checked with
// If-None-Match. A visor that could not answer is not asked again for
// listMissBackoff, so a dial does not wait on the same old peer each time.
const (
	listFreshFor     = 30 * time.Second
	listMissBackoff  = 5 * time.Minute
	listFetchTimeout = 3 * time.Second
	listMaxBody      = 4 << 20
)

type cachedList struct {
	list    *transport.SignedList
	version string
	at      time.Time
	missAt  time.Time
}

// transportListFetcher reads other visors' signed transport lists from their
// :80 over skywire transports only. It never uses a dmsg server, which would
// get 403 anyway.
type transportListFetcher struct {
	v      *Visor
	client *http.Client

	mu    sync.Mutex
	cache map[cipher.PubKey]*cachedList
}

func newTransportListFetcher(v *Visor) *transportListFetcher {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			host, portStr, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			var pk cipher.PubKey
			if err := pk.Set(host); err != nil {
				return nil, err
			}
			port, err := strconv.ParseUint(portStr, 10, 16)
			if err != nil {
				return nil, err
			}
			return v.dialDmsgOverSkynet(ctx, pk, uint16(port))
		},
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     60 * time.Second,
	}
	return &transportListFetcher{v: v, client: &http.Client{Transport: tr}, cache: map[cipher.PubKey]*cachedList{}}
}

// FetchTransportList implements router.TransportListFetcher.
func (f *transportListFetcher) FetchTransportList(ctx context.Context, pk cipher.PubKey) (*transport.SignedList, error) {
	f.mu.Lock()
	c := f.cache[pk]
	if c != nil && c.list != nil && time.Since(c.at) < listFreshFor {
		l := c.list
		f.mu.Unlock()
		return l, nil
	}
	if c != nil && !c.missAt.IsZero() && time.Since(c.missAt) < listMissBackoff {
		f.mu.Unlock()
		return nil, errors.New("transport list: peer did not answer recently")
	}
	var version string
	if c != nil {
		version = c.version
	}
	f.mu.Unlock()

	l, newVersion, err := f.get(ctx, pk, version)
	f.mu.Lock()
	defer f.mu.Unlock()
	if c = f.cache[pk]; c == nil {
		c = &cachedList{}
		f.cache[pk] = c
	}
	if err != nil {
		if ctx.Err() == nil {
			c.missAt = time.Now()
		}
		return nil, err
	}
	c.missAt = time.Time{}
	c.at = time.Now()
	if l != nil {
		c.list, c.version = l, newVersion
	}
	if c.list == nil {
		return nil, errors.New("transport list: not modified but nothing cached")
	}
	return c.list, nil
}

// get fetches pk's list, or returns a nil list when version is still current.
func (f *transportListFetcher) get(ctx context.Context, pk cipher.PubKey, version string) (*transport.SignedList, string, error) {
	ctx, cancel := context.WithTimeout(ctx, listFetchTimeout)
	defer cancel()
	url := fmt.Sprintf("http://%s:%d/transports", pk.Hex(), visorconfig.DmsgHTTPPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	if version != "" {
		req.Header.Set("If-None-Match", `"`+version+`"`)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close() //nolint:errcheck
	switch resp.StatusCode {
	case http.StatusNotModified:
		return nil, version, nil
	case http.StatusOK:
	default:
		return nil, "", fmt.Errorf("transport list from %s: HTTP %d", pk, resp.StatusCode)
	}
	var l transport.SignedList
	if err := json.NewDecoder(io.LimitReader(resp.Body, listMaxBody)).Decode(&l); err != nil {
		return nil, "", fmt.Errorf("transport list from %s: %w", pk, err)
	}
	if l.PK != pk {
		return nil, "", fmt.Errorf("transport list from %s is %s's", pk, l.PK)
	}
	if err := l.Verify(); err != nil {
		return nil, "", err
	}
	return &l, l.Version(), nil
}
