// Package network pkg/transport/network/wt.go c2-net-transport
//
// WT is a first-class skywire transport type: a direct visor-to-visor link over
// WebTransport (HTTP/3 over QUIC), with the peer's https:// endpoint and pinned
// cert hash resolved from a table (like WS, but over WebTransport instead of a
// WebSocket). It is distinct from the dmsg WebTransport carrier, which reaches a
// dmsg server rather than a peer visor.
//
// The server presents a short-lived self-signed cert pinned by SHA-256 — no CA,
// no domain (the dmsg WT cert-hash model). A browser visor can DIAL this
// transport (the browser's WebTransport API + serverCertificateHashes) but
// cannot accept it (no listening socket) — so server-visors run the WT listener
// (wt_native.go) while the dial side is build-tagged: webtransport-go on native,
// the browser-native WebTransport under TinyGo/js (wt_browser.go). The dialed /
// accepted bidirectional stream is adapted to a net.Conn and flows through the
// SAME genericClient initTransport (Noise + yamux) path as every other carrier.
package network

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// WTEntry is a peer's WebTransport dial target: the https:// endpoint URL and
// the lowercase SHA-256 hex of the server's self-signed cert (pinned exactly as
// a browser pins it via serverCertificateHashes).
type WTEntry struct {
	URL      string
	CertHash string
}

// WTTable maps a peer PK to its WebTransport dial target (URL + pinned cert
// hash). It is the WT analog of stcp.PKTable, which carries only a bare address.
// SetEntry allows entries to be added at runtime (e.g. learned from a peer or a
// JS hook), mirroring stcp.PKTable.SetAddr.
type WTTable interface {
	Entry(pk cipher.PubKey) (WTEntry, bool)
	SetEntry(pk cipher.PubKey, e WTEntry)
}

// wtMemoryTable is an in-memory, concurrency-safe WTTable.
type wtMemoryTable struct {
	mu      sync.RWMutex
	entries map[cipher.PubKey]WTEntry
}

// NewWTTable returns an in-memory WTTable seeded with the given entries (may be
// nil for an empty, runtime-populated table).
func NewWTTable(entries map[cipher.PubKey]WTEntry) WTTable {
	if entries == nil {
		entries = make(map[cipher.PubKey]WTEntry)
	}
	return &wtMemoryTable{entries: entries}
}

func (t *wtMemoryTable) Entry(pk cipher.PubKey) (WTEntry, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	e, ok := t.entries[pk]
	return e, ok
}

func (t *wtMemoryTable) SetEntry(pk cipher.PubKey, e WTEntry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries[pk] = e
}

// wtClient is the WT-transport implementation of Client. table maps a peer PK to
// its WebTransport endpoint + pinned cert hash.
type wtClient struct {
	*genericClient
	table WTTable

	// ar is the address-resolver client (addrresolver.APIClient), typed `any` to
	// keep addrresolver out of the TinyGo graph (mirrors wsClient.ar). On native
	// the WT server registers its UDP port + self-signed cert hash with the AR
	// (BindWT) so dialing peers can discover the endpoint AND pin the cert; the
	// dial side resolves the same record. nil on the browser (table-only).
	ar any

	// advertised holds the WT listener's public dial info once Start has run
	// (native only); a browser/TinyGo client never serves, so these stay empty.
	advertisedURL      string
	advertisedCertHash string

	// sharedQUIC, when set, is the *sharedQUICMux the WT server registers its "h3"
	// ALPN handler on, so WT serves over the unified transport_port socket instead
	// of its own UDP port. `any` to keep this untagged file off the quic-go graph
	// (the assertion + use live in wt_native.go); nil → dedicated WT socket.
	sharedQUIC any
}

// AdvertisedWT returns the WT listener's dial URL and pinned cert hash after
// Start has run, for advertising to peers (table/discovery). Both are empty
// before Start or on non-serving (browser/TinyGo) builds.
func (c *wtClient) AdvertisedWT() (url, certHash string) {
	return c.advertisedURL, c.advertisedCertHash
}

func newWT(generic *genericClient, table WTTable) Client {
	client := &wtClient{genericClient: generic, table: table}
	client.netType = types.WT
	return client
}

// ErrWTEntryNotFound is returned when the requested PK has no WT entry in the table.
var ErrWTEntryNotFound = errors.New("wt: entry not found in table")

// Dial implements Client: resolve the peer's WT endpoint + cert hash, dial it
// (build-tagged carrier: webtransport-go on native, the browser WebTransport API
// on TinyGo/js), and wrap the resulting net.Conn in a skywire transport.
func (c *wtClient) Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (Transport, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}

	// Endpoint resolution order: an explicit table entry (browser autoconnect
	// sets the URL+cert hash) dials that single endpoint; otherwise the native AR
	// path (dialResolvedWT) resolves the peer's WT record and dials it LAN-first
	// when same-NAT, falling back to the public endpoint.
	var conn net.Conn
	var err error
	if e, found := c.tableEntry(rPK); found {
		c.log.Debugf("Dialing WT %v @ %s (table)", rPK, e.URL)
		conn, err = wtDial(ctx, e.URL, e.CertHash)
	} else {
		conn, err = c.dialResolvedWT(ctx, rPK)
	}
	if err != nil {
		return nil, err
	}

	tp, err := c.initTransport(ctx, conn, rPK, rPort)
	if err != nil {
		conn.Close() //nolint:errcheck,gosec
		return nil, err
	}
	return tp, nil
}

// tableEntry returns a configured WT endpoint for rPK, guarding a nil table.
func (c *wtClient) tableEntry(rPK cipher.PubKey) (WTEntry, bool) {
	if c.table == nil {
		return WTEntry{}, false
	}
	return c.table.Entry(rPK)
}
