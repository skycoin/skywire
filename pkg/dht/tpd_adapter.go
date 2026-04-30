package dht

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// Salt for transport discovery entries in the DHT.
var tpSalt = []byte("tp")

// tpChunkSize is the target number of transport.Entry items per DHT
// item. Each entry serializes to ~250 bytes (UUID + 2 PKs in hex + a
// few short strings); 200 entries fit in ~50 KB of JSON which leaves
// headroom under the 64 KiB MaxValueSize cap. Hub edges with more
// transports than this overflow into chunked salts ("tp:1", "tp:2"...).
const tpChunkSize = 200

// tpChunkSalt returns the salt name for the i-th tp chunk. Chunk 0 is
// always the bare "tp" salt for backwards compatibility with consumers
// that don't know about chunking.
func tpChunkSalt(i int) []byte {
	if i == 0 {
		return tpSalt
	}
	return []byte(fmt.Sprintf("tp:%d", i))
}

// TPDAdapter implements transport.DiscoveryClient backed by the DHT.
// Each visor publishes its own transport list under its PK with salt
// "tp" (chunk 0). Hub edges with more than tpChunkSize transports
// overflow into chunked salts "tp:1", "tp:2", ...
type TPDAdapter struct {
	node *Node
	log  *logging.Logger
	// prevChunkCount remembers how many tp chunks we wrote last time
	// so the next publish can tombstone the now-unused chunks if the
	// transport list shrinks.
	prevChunkCount atomic.Int32
}

// NewTPDAdapter creates a transport discovery client backed by the DHT.
func NewTPDAdapter(node *Node, log *logging.Logger) *TPDAdapter {
	return &TPDAdapter{node: node, log: log}
}

// RegisterTransports publishes the given transport entries to the DHT.
// All entries are stored as a list of bare transport.Entry under the
// node's own PK. The SignedEntry wrapping that legacy callers pass
// is unwrapped — under-PK DHT publication already authenticates the
// writer at the DHT layer (BEP44-style signed value), so the
// per-entry signatures the SignedEntry carries are redundant. Storing
// bare entries also avoids a misleading all-zeros signatures field
// on V3 publishes (which carry no signatures by design).
func (d *TPDAdapter) RegisterTransports(ctx context.Context, entries ...*transport.SignedEntry) error {
	bare := make([]*transport.Entry, 0, len(entries))
	for _, se := range entries {
		if se == nil || se.Entry == nil {
			continue
		}
		bare = append(bare, se.Entry)
	}
	return d.putEntries(ctx, bare)
}

// RegisterTransportsV3 publishes bare-entry transports to the DHT.
//
// An empty entry list is meaningful: it overwrites any prior state
// at this PK with a fresh sequence so readers stop seeing a stale
// transport set after the visor's last transport went away. Without
// this the DHT entry under our PK would persist indefinitely.
func (d *TPDAdapter) RegisterTransportsV3(ctx context.Context, _ string, entries ...*transport.Entry) error {
	return d.putEntries(ctx, entries)
}

// previousChunkCount tracks how many tp chunk salts we wrote last time.
// On shrink we need to clear the now-unused chunks to avoid stale data.
// Per-adapter state — safe because there's exactly one TPDAdapter per
// visor.
func (d *TPDAdapter) putEntries(ctx context.Context, entries []*transport.Entry) error {
	// Wall-clock nanoseconds as monotonic seq generator. Survives restarts
	// (in-memory entry counts and bandwidth totals do not) and is virtually
	// guaranteed to climb past whatever peers cached for our PK previously.
	// On clock skew the DHT layer's per-peer rejection still keeps things
	// safe — we'll catch up next tick.
	baseSeq := uint64(time.Now().UnixNano())

	// Always publish at least one chunk (chunk 0 = "tp"). Empty entry
	// list still goes through so a "I have no transports anymore"
	// publish overwrites stale state.
	chunks := chunkTpEntries(entries, tpChunkSize)
	if len(chunks) == 0 {
		chunks = [][]*transport.Entry{nil}
	}

	for i, chunk := range chunks {
		data, err := json.Marshal(chunk)
		if err != nil {
			return fmt.Errorf("dht tpd: marshal chunk %d: %w", i, err)
		}
		if len(data) > MaxValueSize {
			// Even a single 200-entry chunk shouldn't hit this — but
			// if a future entry serialization grows, fail loudly
			// rather than silently dropping.
			return fmt.Errorf("dht tpd: chunk %d too large (%d bytes, max %d)", i, len(data), MaxValueSize)
		}
		// Each chunk gets a unique seq so the DHT's monotonic-seq
		// check accepts them all in one pass. (baseSeq + i is fine —
		// next register cycle starts from a fresh nanosecond clock.)
		if err := d.node.Put(ctx, data, baseSeq+uint64(i), tpChunkSalt(i)); err != nil {
			return fmt.Errorf("dht tpd: put chunk %d: %w", i, err)
		}
	}

	// Tombstone any chunks we wrote in a previous, larger publish.
	// Publishing an empty array advances the seq past the stale data
	// so readers stop seeing it.
	prev := int(d.prevChunkCount.Load())
	for i := len(chunks); i < prev; i++ {
		_ = d.node.Put(ctx, []byte("[]"), baseSeq+uint64(i), tpChunkSalt(i)) //nolint:errcheck
	}
	d.prevChunkCount.Store(int32(len(chunks))) //nolint:gosec

	return nil
}

// chunkTpEntries splits entries into ~chunkSize-element groups so a
// single DHT item never crosses MaxValueSize. Returns nil for an
// empty input (caller decides whether to publish an empty chunk 0
// for cleanup).
func chunkTpEntries(entries []*transport.Entry, chunkSize int) [][]*transport.Entry {
	if len(entries) == 0 {
		return nil
	}
	if chunkSize <= 0 {
		chunkSize = tpChunkSize
	}
	n := (len(entries) + chunkSize - 1) / chunkSize
	chunks := make([][]*transport.Entry, 0, n)
	for i := 0; i < len(entries); i += chunkSize {
		end := i + chunkSize
		if end > len(entries) {
			end = len(entries)
		}
		chunks = append(chunks, entries[i:end])
	}
	return chunks
}

// GetTransportByID searches the DHT for a transport by ID.
// This is expensive — requires fetching transport lists from multiple visors.
// Returns ErrNotFound if not located.
func (d *TPDAdapter) GetTransportByID(ctx context.Context, id uuid.UUID) (*transport.Entry, error) {
	// The DHT stores transport lists per-visor, not per-transport.
	// A full lookup by ID would require iterating all visors.
	// This is best handled by the HTTP fallback.
	return nil, fmt.Errorf("dht tpd: GetTransportByID not supported (use HTTP fallback)")
}

// GetTransportsByEdge returns all transports for a given visor PK.
//
// Reads chunk 0 (salt "tp") plus any chunked overflow salts ("tp:1",
// "tp:2", ...) until one is missing. Decodes the bare []*Entry shape
// produced by current writers; falls through to the legacy
// []*SignedEntry shape so peers that haven't republished since the
// chunking upgrade still resolve.
func (d *TPDAdapter) GetTransportsByEdge(ctx context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	var out []*transport.Entry
	for i := 0; ; i++ {
		item, err := d.node.Get(ctx, pk, tpChunkSalt(i))
		if err != nil {
			if i == 0 {
				return nil, err
			}
			// Stopping at the first chunk-not-found is the cheap way
			// to detect "no more chunks." A torn write (chunk 0 and 2
			// present, 1 missing) would mean a partial publish — but
			// our writer always writes chunks contiguously, and reads
			// race with a writer at most one publish behind, so this
			// is safe.
			if errors.Is(err, ErrNotFound) {
				break
			}
			return nil, err
		}
		entries, err := decodeTpItem(item.V, pk)
		if err != nil {
			return nil, fmt.Errorf("dht tpd: decode chunk %d: %w", i, err)
		}
		out = append(out, entries...)
		// An empty chunk means we hit a tombstone — older publishes
		// had more chunks but the writer shrank. Stop reading further.
		if len(entries) == 0 && i > 0 {
			break
		}
	}
	return out, nil
}

// DecodeTpItem is the public wrapper around decodeTpItem for callers
// outside this package (e.g. cmd/skywire-cli/commands/route/calc.go's
// scan over all tp entries). srcPK is the visor PK whose entry this
// is — used to fill in the source for compact shapes that don't
// carry it explicitly.
func DecodeTpItem(v []byte, srcPK cipher.PubKey) ([]*transport.Entry, error) {
	return decodeTpItem(v, srcPK)
}

// decodeTpItem unmarshals one tp-chunk value into entries, accepting
// any of the four shapes that coexist on the wire under the tp salt:
//
//  1. Bare:             []transport.Entry             (canonical)
//  2. Signed:           []transport.SignedEntry       (legacy wrapper)
//  3. Compact-array:    [{r, t, l}]                   (deployment mirrors)
//  4. Compact-envelope: {s: <pk>, ts: [{r, t, l}]}    (recommended for size)
//
// srcPK is the visor PK whose tp entry this is — used as the source
// for compact shapes that don't carry it (compact-array, compact-
// envelope without `s`). Callers from GetTransportsByEdge always know
// it because it's the lookup key.
//
// Compact entries are synthesized into transport.Entry with a
// deterministic ID (transport.MakeTransportID(src, remote, type)).
func decodeTpItem(v []byte, srcPK cipher.PubKey) ([]*transport.Entry, error) {
	// Bare canonical.
	var entries []*transport.Entry
	if err := json.Unmarshal(v, &entries); err == nil && (len(entries) == 0 || entries[0] != nil) {
		// Distinguish bare from compact-array: both unmarshal into a
		// slice, but compact-array produces zero-valued Edges. Bare
		// always has populated Edges.
		anyResolved := false
		for _, e := range entries {
			if e == nil {
				continue
			}
			if !e.Edges[0].Null() && !e.Edges[1].Null() {
				anyResolved = true
				break
			}
		}
		if anyResolved {
			return entries, nil
		}
	}

	// Signed wrapper.
	var signed []*transport.SignedEntry
	if err := json.Unmarshal(v, &signed); err == nil {
		out := make([]*transport.Entry, 0, len(signed))
		for _, se := range signed {
			if se != nil && se.Entry != nil &&
				!se.Entry.Edges[0].Null() && !se.Entry.Edges[1].Null() {
				out = append(out, se.Entry)
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}

	// Compact-envelope: explicit source PK in the value.
	var env compactEnvelope
	if json.Unmarshal(v, &env) == nil && env.Source != "" {
		var pk cipher.PubKey
		if err := pk.Set(env.Source); err == nil {
			out := make([]*transport.Entry, 0, len(env.Transports))
			for _, c := range env.Transports {
				if e := compactToEntry(pk, c); e != nil {
					out = append(out, e)
				}
			}
			return out, nil
		}
	}

	// Compact-array: source PK comes from the lookup key (srcPK).
	var compact []compactTpEntry
	if json.Unmarshal(v, &compact) == nil && len(compact) > 0 && srcPK != (cipher.PubKey{}) {
		out := make([]*transport.Entry, 0, len(compact))
		for _, c := range compact {
			if e := compactToEntry(srcPK, c); e != nil {
				out = append(out, e)
			}
		}
		return out, nil
	}

	return nil, fmt.Errorf("dht tpd: value matches no known tp shape (bare/signed/compact-array/compact-envelope)")
}

// compactTpEntry is one row of the single-letter compact format that
// some publishers emit under the tp salt: r=remote PK, t=transport
// type, l=latency in ms.
type compactTpEntry struct {
	Remote  string  `json:"r"`
	Type    string  `json:"t"`
	Latency float64 `json:"l,omitempty"`
}

// compactEnvelope wraps a list of compact entries with their source
// PK. The recommended publish format for size-conscious publishers:
// one source PK per visor, not per transport, so hub edges fit
// comfortably under MaxValueSize.
type compactEnvelope struct {
	Source     string           `json:"s"`
	Transports []compactTpEntry `json:"ts"`
}

// compactToEntry synthesizes a transport.Entry from a compact row
// plus a known source PK. Returns nil if the row is malformed.
func compactToEntry(srcPK cipher.PubKey, c compactTpEntry) *transport.Entry {
	if c.Remote == "" || c.Type == "" {
		return nil
	}
	var rPK cipher.PubKey
	if err := rPK.Set(c.Remote); err != nil {
		return nil
	}
	tpType := tptypes.Type(c.Type)
	return &transport.Entry{
		ID:      transport.MakeTransportID(srcPK, rPK, tpType),
		Edges:   transport.SortEdges(srcPK, rPK),
		Type:    tpType,
		Latency: c.Latency,
	}
}

// GetAllTransports is not efficiently supported by the DHT.
func (d *TPDAdapter) GetAllTransports(_ context.Context) ([]*transport.Entry, error) {
	return nil, nil
}

// GetTransportStats is not supported by the DHT.
func (d *TPDAdapter) GetTransportStats(_ context.Context, _ cipher.PubKey) (*transport.TransportStats, error) {
	return nil, fmt.Errorf("dht tpd: stats not supported")
}

// GetAllTransportsStats is not supported by the DHT.
func (d *TPDAdapter) GetAllTransportsStats(_ context.Context) (*transport.NetworkTransportStats, error) {
	return nil, fmt.Errorf("dht tpd: stats not supported")
}

// GetAllTransportsPerKeyStats is not supported by the DHT.
func (d *TPDAdapter) GetAllTransportsPerKeyStats(_ context.Context) (transport.PerKeyStats, error) {
	return nil, fmt.Errorf("dht tpd: stats not supported")
}

// DeleteTransport publishes an updated transport list without the given ID.
// In practice, the visor's transport manager handles this locally and
// the next RegisterTransports call will reflect the removal.
func (d *TPDAdapter) DeleteTransport(_ context.Context, _ uuid.UUID) error {
	return nil // no-op: next register cycle will publish updated list
}

// DeleteTransports is a batch version of DeleteTransport.
func (d *TPDAdapter) DeleteTransports(_ context.Context, _ []uuid.UUID) (int, error) {
	return 0, nil
}

// HybridTPDClient wraps a DHT adapter with an HTTP fallback.
type HybridTPDClient struct {
	dht  *TPDAdapter
	http transport.DiscoveryClient
	log  *logging.Logger
}

// NewHybridTPDClient creates a hybrid transport discovery client.
func NewHybridTPDClient(dhtAdapter *TPDAdapter, httpClient transport.DiscoveryClient, log *logging.Logger) *HybridTPDClient {
	return &HybridTPDClient{dht: dhtAdapter, http: httpClient, log: log}
}

// RegisterTransports writes to both DHT and HTTP.
func (h *HybridTPDClient) RegisterTransports(ctx context.Context, entries ...*transport.SignedEntry) error {
	if err := h.dht.RegisterTransports(ctx, entries...); err != nil {
		h.log.WithError(err).Debug("DHT RegisterTransports failed")
	}
	return h.http.RegisterTransports(ctx, entries...)
}

// RegisterTransportsV3 writes bare entries to both DHT and HTTP.
func (h *HybridTPDClient) RegisterTransportsV3(ctx context.Context, version string, entries ...*transport.Entry) error {
	if err := h.dht.RegisterTransportsV3(ctx, version, entries...); err != nil {
		h.log.WithError(err).Debug("DHT RegisterTransportsV3 failed")
	}
	return h.http.RegisterTransportsV3(ctx, version, entries...)
}

// GetTransportByID tries HTTP (DHT can't efficiently look up by ID).
func (h *HybridTPDClient) GetTransportByID(ctx context.Context, id uuid.UUID) (*transport.Entry, error) {
	return h.http.GetTransportByID(ctx, id)
}

// GetTransportsByEdge tries DHT first, falls back to HTTP.
func (h *HybridTPDClient) GetTransportsByEdge(ctx context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	entries, err := h.dht.GetTransportsByEdge(ctx, pk)
	if err == nil && len(entries) > 0 {
		return entries, nil
	}
	return h.http.GetTransportsByEdge(ctx, pk)
}

// GetAllTransports delegates to HTTP.
func (h *HybridTPDClient) GetAllTransports(ctx context.Context) ([]*transport.Entry, error) {
	return h.http.GetAllTransports(ctx)
}

// GetTransportStats delegates to HTTP.
func (h *HybridTPDClient) GetTransportStats(ctx context.Context, pk cipher.PubKey) (*transport.TransportStats, error) {
	return h.http.GetTransportStats(ctx, pk)
}

// GetAllTransportsStats delegates to HTTP.
func (h *HybridTPDClient) GetAllTransportsStats(ctx context.Context) (*transport.NetworkTransportStats, error) {
	return h.http.GetAllTransportsStats(ctx)
}

// GetAllTransportsPerKeyStats delegates to HTTP.
func (h *HybridTPDClient) GetAllTransportsPerKeyStats(ctx context.Context) (transport.PerKeyStats, error) {
	return h.http.GetAllTransportsPerKeyStats(ctx)
}

// DeleteTransport writes to both.
func (h *HybridTPDClient) DeleteTransport(ctx context.Context, id uuid.UUID) error {
	return h.http.DeleteTransport(ctx, id)
}

// DeleteTransports writes to both.
func (h *HybridTPDClient) DeleteTransports(ctx context.Context, ids []uuid.UUID) (int, error) {
	return h.http.DeleteTransports(ctx, ids)
}
