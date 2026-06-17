// Package dmsgclient pkg/dmsgclient/cli_fallback.go
package dmsgclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
)

// StartDmsgWithSyntheticDiscovery starts dmsg with a synthetic discovery entry for the discovery server itself
func StartDmsgWithSyntheticDiscovery(ctx context.Context, dlog *logging.Logger, pk cipher.PubKey, sk cipher.SecKey, httpClient *http.Client, dmsgDisc string, dmsgSessions int) (dmsgC *dmsg.Client, stop func(), err error) {
	if dlog == nil {
		return nil, nil, fmt.Errorf("nil logger")
	}

	// Create base discovery client
	baseDiscClient := disc.NewHTTP(dmsgDisc, httpClient, dlog)

	// Wrap with caching client that includes synthetic entry for discovery server
	discPK := dmsg.ExtractPKFromDmsgAddr(dmsgDisc)
	if discPK != "" {
		var discoveryPK cipher.PubKey
		if err := discoveryPK.UnmarshalText([]byte(discPK)); err == nil {
			// Get all available dmsg servers as delegated servers
			var delegatedServers []cipher.PubKey
			for _, server := range dmsg.Prod.DmsgServers {
				delegatedServers = append(delegatedServers, server.Static)
			}
			syntheticEntry := &disc.Entry{
				Version: "0.0.1",
				Static:  discoveryPK,
				Client: &disc.Client{
					DelegatedServers: delegatedServers,
				},
			}
			baseDiscClient = newCachingDiscClient(baseDiscClient, syntheticEntry, dlog)
			dlog.Debug("Created synthetic discovery entry for dialing")
		}
	}

	dmsgC = dmsg.NewClient(pk, sk, baseDiscClient, &dmsg.Config{MinSessions: dmsgSessions})
	dlog.Debug("Created dmsg client.")

	go dmsgC.Serve(ctx)
	dlog.Debug("dmsgclient.Serve(ctx)")

	stop = func() {
		err := dmsgC.Close()
		dlog.WithError(err).Debug("Disconnected from dmsg network.\n")
		log.Println()
	}
	dlog.WithField("dmsg_disc", dmsgDisc).Debug("Connecting to dmsg network...\n")
	dlog.WithField("client public_key", pk.String()).Debug("\n")
	select {
	case <-ctx.Done():
		stop()
		return nil, nil, ctx.Err()

	case <-dmsgC.Ready():
		dlog.Debug("Dmsg network ready.")
		return dmsgC, stop, nil
	}
}

// StartDmsgWithDirectClient starts dmsg with a fallback discovery client
// This allows dialing any client including the discovery server which doesn't register itself
// It uses direct client for known entries (servers, discovery, local client) and falls back
// to HTTP discovery for unknown entries (arbitrary target clients)
func StartDmsgWithDirectClient(ctx context.Context, dlog *logging.Logger, pk cipher.PubKey, sk cipher.SecKey, dmsgSessions int) (dmsgC *dmsg.Client, stop func(), err error) {
	if dlog == nil {
		return nil, nil, fmt.Errorf("nil logger")
	}

	// Build entries for all dmsg servers
	var entries []*disc.Entry
	for _, server := range dmsg.Prod.DmsgServers {
		entries = append(entries, &server)
	}

	// Add synthetic entry for discovery server
	discPK := dmsg.ExtractPKFromDmsgAddr(DmsgDiscAddr)
	if discPK != "" {
		var discoveryPK cipher.PubKey
		if err := discoveryPK.UnmarshalText([]byte(discPK)); err == nil {
			var delegatedServers []cipher.PubKey
			for _, server := range dmsg.Prod.DmsgServers {
				delegatedServers = append(delegatedServers, server.Static)
			}
			discoveryEntry := &disc.Entry{
				Version: "0.0.1",
				Static:  discoveryPK,
				Client: &disc.Client{
					DelegatedServers: delegatedServers,
				},
			}
			entries = append(entries, discoveryEntry)
			dlog.Debug("Added synthetic discovery entry to direct client")
		}
	}

	// Add synthetic entry for our own client
	var delegatedServers []cipher.PubKey
	for _, server := range dmsg.Prod.DmsgServers {
		delegatedServers = append(delegatedServers, server.Static)
	}
	clientEntry := &disc.Entry{
		Version: "0.0.1",
		Static:  pk,
		Client: &disc.Client{
			DelegatedServers: delegatedServers,
		},
	}
	entries = append(entries, clientEntry)

	// Create direct client with known entries
	directClient := direct.NewClient(entries, dlog)

	// Create HTTP discovery client as fallback for unknown entries
	httpDiscClient := disc.NewHTTP(DmsgDiscURL, &http.Client{}, dlog)

	// Wrap with fallback client that tries direct first, then HTTP discovery
	fallbackClient := NewFallbackDiscClient(directClient, httpDiscClient, dlog)

	dmsgC = dmsg.NewClient(pk, sk, fallbackClient, &dmsg.Config{MinSessions: dmsgSessions})
	dlog.Debug("Created dmsg client with fallback discovery client (direct + HTTP).")

	go dmsgC.Serve(ctx)
	dlog.Debug("dmsgclient.Serve(ctx)")

	stop = func() {
		err := dmsgC.Close()
		dlog.WithError(err).Debug("Disconnected from dmsg network.\n")
		log.Println()
	}
	dlog.Debug("Connecting to dmsg network...\n")
	dlog.WithField("client public_key", pk.String()).Debug("\n")
	select {
	case <-ctx.Done():
		stop()
		return nil, nil, ctx.Err()

	case <-dmsgC.Ready():
		dlog.Debug("Dmsg network ready.")
		return dmsgC, stop, nil
	}
}

// StartDmsgSelfHostedDisc starts a SINGLE dmsg client that reaches the
// (dmsg-only) discovery over its OWN sessions — instead of the previous default
// path, which spun up N separate "bootstrap" direct clients sharing this same
// key just to carry discovery HTTP. A dmsg server permits one session per PK, so
// those bootstrap clients and the main client kicked each other off every shared
// server, producing a continuous reconnect/re-dial storm (observed on
// dmsgweb-surveys at ~30 session events/sec).
//
// How it avoids the bootstrap clients: a direct disc client is preloaded with
// every dmsg server entry plus a synthetic entry for the discovery server, so
// this one client can connect to servers AND dial the discovery server without
// any prior discovery lookup. Discovery HTTP (peer lookups) is then routed over
// this client's own sessions via dmsghttp. PutEntry is a no-op through the
// fallback wrapper, so the client doesn't register itself — it only dials out.
// Reaching direct/hidden services still works through DialStream's existing
// dmsg-100 fallback (dialViaConnectedServers over the same sessions).
func StartDmsgSelfHostedDisc(ctx context.Context, dlog *logging.Logger, pk cipher.PubKey, sk cipher.SecKey, dmsgDiscAddr string, dmsgSessions int) (dmsgC *dmsg.Client, stop func(), err error) {
	if dlog == nil {
		return nil, nil, fmt.Errorf("nil logger")
	}

	// Preload direct entries: every dmsg server (so AvailableServers works with
	// no discovery round-trip) plus a synthetic entry for the discovery server
	// (so we can dial it directly over dmsg).
	var entries []*disc.Entry
	var delegatedServers []cipher.PubKey
	for i := range dmsg.Prod.DmsgServers {
		entries = append(entries, &dmsg.Prod.DmsgServers[i])
		delegatedServers = append(delegatedServers, dmsg.Prod.DmsgServers[i].Static)
	}
	if discPK := dmsg.ExtractPKFromDmsgAddr(dmsgDiscAddr); discPK != "" {
		var discoveryPK cipher.PubKey
		if uerr := discoveryPK.UnmarshalText([]byte(discPK)); uerr == nil {
			entries = append(entries, &disc.Entry{
				Version: "0.0.1",
				Static:  discoveryPK,
				Client:  &disc.Client{DelegatedServers: delegatedServers},
			})
		}
	}
	// Synthetic entry for our OWN client. Without it the client's start-up
	// self-lookup misses the direct client and falls back to an HTTP-disc dial of
	// the discovery server before any session can reach it — which stalls Ready.
	entries = append(entries, &disc.Entry{
		Version: "0.0.1",
		Static:  pk,
		Client:  &disc.Client{DelegatedServers: delegatedServers},
	})
	directClient := direct.NewClient(entries, dlog)

	// dmsgSessions <= 0 means "connect to all servers" (e.g. `dmsg web -e 0`),
	// not an error. MinSessions is the number of sessions the client must
	// establish before Ready; capping it at the known server count keeps the
	// client from blocking forever waiting for more sessions than exist.
	if dmsgSessions <= 0 || dmsgSessions > len(dmsg.Prod.DmsgServers) {
		dmsgSessions = len(dmsg.Prod.DmsgServers)
	}

	// HTTP discovery fallback (for looking up arbitrary regular peers) rides this
	// client's own sessions. The transport is bound after the client is created;
	// no request is issued before Serve starts below, so the empty Transport is
	// never used.
	httpClient := &http.Client{}
	httpDisc := disc.NewHTTP(dmsgDiscAddr, httpClient, dlog)
	fallbackDisc := NewFallbackDiscClient(directClient, httpDisc, dlog)

	dmsgC = dmsg.NewClient(pk, sk, fallbackDisc, &dmsg.Config{MinSessions: dmsgSessions})
	httpClient.Transport = dmsghttp.MakeHTTPTransport(ctx, dmsgC)
	dlog.Debug("Created single dmsg client with self-hosted discovery over its own sessions.")

	go dmsgC.Serve(ctx)

	stop = func() {
		cerr := dmsgC.Close()
		dlog.WithError(cerr).Debug("Disconnected from dmsg network.")
	}
	select {
	case <-ctx.Done():
		stop()
		return nil, nil, ctx.Err()
	case <-dmsgC.Ready():
		dlog.Debug("Dmsg network ready.")
		return dmsgC, stop, nil
	}
}

// cachingDiscClient wraps a discovery client and caches a synthetic entry
type cachingDiscClient struct {
	base           disc.APIClient
	syntheticEntry *disc.Entry
	log            *logging.Logger
}

// newCachingDiscClient creates a discovery client that caches a synthetic entry
func newCachingDiscClient(base disc.APIClient, syntheticEntry *disc.Entry, log *logging.Logger) disc.APIClient {
	return &cachingDiscClient{
		base:           base,
		syntheticEntry: syntheticEntry,
		log:            log,
	}
}

// Entry returns the synthetic entry if PK matches, otherwise queries base client
func (c *cachingDiscClient) Entry(ctx context.Context, pk cipher.PubKey) (*disc.Entry, error) {
	if c.syntheticEntry != nil && c.syntheticEntry.Static == pk {
		c.log.WithField("pk", pk.String()).Debug("Returning synthetic discovery entry")
		return c.syntheticEntry, nil
	}
	return c.base.Entry(ctx, pk)
}

// PostEntry delegates to base client
func (c *cachingDiscClient) PostEntry(ctx context.Context, entry *disc.Entry) error {
	return c.base.PostEntry(ctx, entry)
}

// PutEntry delegates to base client
func (c *cachingDiscClient) PutEntry(ctx context.Context, sk cipher.SecKey, entry *disc.Entry) error {
	return c.base.PutEntry(ctx, sk, entry)
}

// DelEntry delegates to base client
func (c *cachingDiscClient) DelEntry(ctx context.Context, entry *disc.Entry) error {
	return c.base.DelEntry(ctx, entry)
}

// AvailableServers delegates to base client
func (c *cachingDiscClient) AvailableServers(ctx context.Context) ([]*disc.Entry, error) {
	return c.base.AvailableServers(ctx)
}

// AllServers delegates to base client
func (c *cachingDiscClient) AllServers(ctx context.Context) ([]*disc.Entry, error) {
	return c.base.AllServers(ctx)
}

// AllEntries delegates to base client
func (c *cachingDiscClient) AllEntries(ctx context.Context) ([]string, error) {
	return c.base.AllEntries(ctx)
}

// AllClientsByServer delegates to base client
func (c *cachingDiscClient) AllClientsByServer(ctx context.Context) (map[string][]*disc.Entry, error) {
	return c.base.AllClientsByServer(ctx)
}

// ClientsByServer delegates to base client
func (c *cachingDiscClient) ClientsByServer(ctx context.Context, serverPK cipher.PubKey) ([]*disc.Entry, error) {
	return c.base.ClientsByServer(ctx, serverPK)
}

// fallbackDiscClient tries direct client first, falls back to HTTP discovery for unknown entries
type fallbackDiscClient struct {
	direct disc.APIClient
	http   disc.APIClient
	log    *logging.Logger
	// register routes the WRITE methods (Put/Post/DelEntry). When false
	// (the service variant), writes go to the direct client, which no-ops —
	// services run as direct clients and don't publish to dmsg-discovery.
	// When true (the visor variant), writes go to the HTTP client so the
	// caller's entry IS published to the real dmsg-discovery, while READS
	// still resolve direct-first. This lets one client both register itself
	// AND resolve seeded service/server PKs statically (no HTTP-over-dmsg
	// round-trip — the round-trip to the entry-less, root-of-trust dmsg-disc
	// is what hot-loops when the whole disc is dmsgfirst).
	register bool
}

// NewFallbackDiscClient creates a discovery client that tries direct first, then HTTP.
// Used by callers that need to dial arbitrary client PKs registered in the real
// dmsg-discovery while keeping a pre-loaded direct.Client for the bootstrap server
// list (and any synthetic/seeded entries the caller wants to short-circuit).
// WRITES no-op (direct) — the non-registering "service" variant.
func NewFallbackDiscClient(direct, http disc.APIClient, log *logging.Logger) disc.APIClient {
	return &fallbackDiscClient{
		direct: direct,
		http:   http,
		log:    log,
	}
}

// NewRegisteringFallbackDiscClient is the visor variant: READS resolve
// direct-first (seeded service/server PKs short-circuit, no HTTP-over-dmsg
// round-trip), but WRITES (Put/Post/DelEntry) go to the HTTP client so the
// visor's own entry IS published to the real dmsg-discovery. This lets a
// SINGLE dmsg client both register itself and resolve bootstrap PKs
// statically — replacing the two-client (dmsgC discovery + dmsgDC direct)
// setup whose shared PK self-evicts on the dmsg servers.
func NewRegisteringFallbackDiscClient(direct, http disc.APIClient, log *logging.Logger) disc.APIClient {
	return &fallbackDiscClient{
		direct:   direct,
		http:     http,
		log:      log,
		register: true,
	}
}

// Entry tries direct client first, falls back to HTTP for unknown entries
func (f *fallbackDiscClient) Entry(ctx context.Context, pk cipher.PubKey) (*disc.Entry, error) {
	// Try direct client first
	entry, err := f.direct.Entry(ctx, pk)
	if err == nil && entry.Static == pk {
		return entry, nil
	}

	// Fall back to HTTP discovery for unknown entries
	f.log.WithField("pk", pk.String()).Debug("Entry not in direct client, querying HTTP discovery")
	return f.http.Entry(ctx, pk)
}

// PostEntry: registering variant publishes to HTTP discovery; service
// variant no-ops via the direct client.
func (f *fallbackDiscClient) PostEntry(ctx context.Context, entry *disc.Entry) error {
	if f.register {
		return f.http.PostEntry(ctx, entry)
	}
	return f.direct.PostEntry(ctx, entry)
}

// PutEntry delegates to the direct client (which no-ops). Services
// using this fallback wrapper run as direct dmsg clients and are not
// supposed to register themselves in dmsg-discovery — they are
// reachable equally through any dmsg server via the consumer's
// preloaded direct.Client. Routing PutEntry to HTTP caused TD/SD/
// dmsg-disc to publish themselves on every dmsg.Client UpdateInterval
// (5 min default for clients), producing entries with seq numbers
// growing into the hundreds and pointless write traffic against
// dmsg-discovery; consumers gain nothing from those entries because
// the visor's direct.Client already knows the service PK → all-servers
// mapping at startup.
func (f *fallbackDiscClient) PutEntry(ctx context.Context, sk cipher.SecKey, entry *disc.Entry) error {
	if f.register {
		return f.http.PutEntry(ctx, sk, entry)
	}
	return f.direct.PutEntry(ctx, sk, entry)
}

// DelEntry: registering variant removes from HTTP discovery; service
// variant no-ops via the direct client.
func (f *fallbackDiscClient) DelEntry(ctx context.Context, entry *disc.Entry) error {
	if f.register {
		return f.http.DelEntry(ctx, entry)
	}
	return f.direct.DelEntry(ctx, entry)
}

// AvailableServers delegates to direct client
func (f *fallbackDiscClient) AvailableServers(ctx context.Context) ([]*disc.Entry, error) {
	return f.direct.AvailableServers(ctx)
}

// AllServers delegates to direct client
func (f *fallbackDiscClient) AllServers(ctx context.Context) ([]*disc.Entry, error) {
	return f.direct.AllServers(ctx)
}

// AllEntries delegates to direct client
func (f *fallbackDiscClient) AllEntries(ctx context.Context) ([]string, error) {
	return f.direct.AllEntries(ctx)
}

// AllClientsByServer delegates to HTTP client
func (f *fallbackDiscClient) AllClientsByServer(ctx context.Context) (map[string][]*disc.Entry, error) {
	return f.http.AllClientsByServer(ctx)
}

// ClientsByServer delegates to HTTP client
func (f *fallbackDiscClient) ClientsByServer(ctx context.Context, serverPK cipher.PubKey) ([]*disc.Entry, error) {
	return f.http.ClientsByServer(ctx, serverPK)
}

// FallbackRoundTripper tries multiple DMSG transports until one succeeds.
type FallbackRoundTripper struct {
	ctx     context.Context
	clients []*dmsg.Client
}

// NewFallbackRoundTripper initializes the fallback round tripper.
func NewFallbackRoundTripper(ctx context.Context, clients []*dmsg.Client) http.RoundTripper {
	return &FallbackRoundTripper{
		ctx:     ctx,
		clients: clients,
	}
}

// RoundTrip tries each DMSG client in order until a successful response is received.
func (f *FallbackRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Buffer the request body so it can be replayed on retry.
	// Without this, the first failed transport consumes the body
	// and subsequent transports receive an empty body.
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body for retry: %w", err)
		}
		req.Body.Close() //nolint:errcheck,gosec
	}

	var lastErr error
	for _, client := range f.clients {
		// Reset the body for each attempt
		if bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		} else {
			req.Body = nil
		}

		rt := dmsghttp.MakeHTTPTransport(f.ctx, client)
		resp, err := rt.RoundTrip(req)
		if err != nil {
			lastErr = err
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("all DMSG transports failed: last error: %w", lastErr)
}
