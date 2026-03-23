// Package dmsgclient pkg/dmsgclient/cli_fallback.go
package dmsgclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"

	"github.com/skycoin/dmsg/pkg/direct"
	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
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
	fallbackClient := newFallbackDiscClient(directClient, httpDiscClient, dlog)

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
}

// newFallbackDiscClient creates a discovery client that tries direct first, then HTTP
func newFallbackDiscClient(direct, http disc.APIClient, log *logging.Logger) disc.APIClient {
	return &fallbackDiscClient{
		direct: direct,
		http:   http,
		log:    log,
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

// PostEntry delegates to direct client
func (f *fallbackDiscClient) PostEntry(ctx context.Context, entry *disc.Entry) error {
	return f.direct.PostEntry(ctx, entry)
}

// PutEntry delegates to HTTP client (direct client doesn't support updates)
func (f *fallbackDiscClient) PutEntry(ctx context.Context, sk cipher.SecKey, entry *disc.Entry) error {
	return f.http.PutEntry(ctx, sk, entry)
}

// DelEntry delegates to direct client
func (f *fallbackDiscClient) DelEntry(ctx context.Context, entry *disc.Entry) error {
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
