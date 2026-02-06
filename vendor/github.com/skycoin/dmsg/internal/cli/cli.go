// Package cli internal/cli/go
package cli

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"

	"github.com/skycoin/dmsg/internal/flags"
	"github.com/skycoin/dmsg/pkg/direct"
	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
)

/*
Default mode of operation is dmsghttp:
* Start dmsg-direct client ; connect directly to a dmsg server
* HTTP client is configured with a dmsg HTTP transport provided by the dmsg-direct client
* HTTP client is used to make HTTP GET request to '/health' of dmsg discovery dmsg address
* If the dmsg-discovery is unreachable via the configured http client:
	- Shuffle dmsg servers
	- Re-make dmsg direct clent
	- Reconfigure HTTP client with dmsg HTTP transport provided by the dmsg-direct client
	- Fetch '/health' from dmsg discovery dmsg address [<pk>:<port>]
	- Repeat the previous 4 steps on error / until no error
* Start dmsghttp client
* Connect to dmsg client address (if specified)

'-Z' flag: use plain http to connect to dmsg-discovery
* HTTP client is used to make HTTP GET request to '/health' of dmsg discovery URL
* Start dmsg client
* Connect to dmsg client address (if specified)

'-B' flag: use dmsg direct client
* Start dmsg-direct client
* Connect to dmsg client address (if specified)
*/

// InitDmsgWithFlags starts dmsg with flags from the flags package
func InitDmsgWithFlags(ctx context.Context, dlog *logging.Logger, pk cipher.PubKey, sk cipher.SecKey, httpClient *http.Client, destination string) (dmsgC *dmsg.Client, stop func(), err error) {
	if flags.UseDC {
		return StartDmsgDirect(ctx, dlog, pk, sk, "", flags.DmsgSessions, dmsg.ExtractPKFromDmsgAddr(destination))
	}
	if flags.UseHTTP {
		resp, err := httpClient.Get(flags.DmsgDiscURL + "/health")
		if err != nil {
			dlog.WithError(err).Fatal("Error connecting to dmsg-discovery with http client")
		}
		defer resp.Body.Close() //nolint

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			dlog.WithError(err).Error("Failed to read response body from discovery")
		} else {
			dlog.Infof("Received response from dmsg-discovery server %s/health:\n%s", flags.DmsgDiscURL, string(body))
		}

		// Use direct client with synthetic entries for discovery server and all dmsg servers
		// This allows dialing the discovery server which doesn't register itself
		return StartDmsgWithDirectClient(ctx, dlog, pk, sk, flags.DmsgSessions)
	}

	// Default dmsghttp mode
	var dmsgHTTP *http.Client
	var dmsgClients []*dmsg.Client
	var closeFns []func()

	dlog.Debug("Starting DMSG direct clients.")
	for _, server := range dmsg.Prod.DmsgServers {
		if len(dmsgClients) >= flags.DmsgSessions {
			break
		}

		dmsgDC, closeFn, err := StartDmsgDirectWithServers(ctx, dlog, pk, sk, flags.DmsgDiscAddr, []*disc.Entry{&server}, flags.DmsgSessions, dmsg.ExtractPKFromDmsgAddr(flags.DmsgDiscAddr))
		if err != nil {
			dlog.WithError(err).Error("Failed to start DMSG direct client. Skipping server...")
			continue
		}

		dmsgClients = append(dmsgClients, dmsgDC)
		closeFns = append(closeFns, closeFn)
	}

	if len(dmsgClients) == 0 {
		dlog.Fatal("Failed to start any DMSG direct clients.")
	}

	// Build HTTP client with fallback round tripper
	dmsgHTTP = &http.Client{
		Transport: NewFallbackRoundTripper(ctx, dmsgClients),
	}

	dlog.Debug("Checking discovery /health using DMSG HTTP client.")
	resp, err := dmsgHTTP.Get(flags.DmsgDiscAddr + "/health")
	if err != nil {
		for _, fn := range closeFns {
			fn()
		}
		dlog.WithError(err).Fatal("All DMSG transports failed to reach discovery /health")
	}
	defer resp.Body.Close() //nolint

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		dlog.WithError(err).Error("Failed to read discovery /health response body")
	} else {
		dlog.Infof("Received response from dmsg-discovery server %s/health:\n%s", flags.DmsgDiscAddr, string(body))
	}

	return StartDmsgWithSyntheticDiscovery(ctx, dlog, pk, sk, dmsgHTTP, flags.DmsgDiscAddr, flags.DmsgSessions)
}

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

// StartDmsg starts dmsg returns a dmsg client for the given dmsg discovery
func StartDmsg(ctx context.Context, dlog *logging.Logger, pk cipher.PubKey, sk cipher.SecKey, httpClient *http.Client, dmsgDisc string, dmsgSessions int) (dmsgC *dmsg.Client, stop func(), err error) {
	if dlog == nil {
		return nil, nil, fmt.Errorf("nil logger")
	}

	dmsgC = dmsg.NewClient(pk, sk, disc.NewHTTP(dmsgDisc, httpClient, dlog), &dmsg.Config{MinSessions: dmsgSessions})
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

// StartDmsgDirect starts dmsg returns a dmsg direct client
func StartDmsgDirect(ctx context.Context, dlog *logging.Logger, pk cipher.PubKey, sk cipher.SecKey, dmsgDiscAddr string, dmsgSessions int, destination string) (*dmsg.Client, func(), error) {
	if len(dmsg.Prod.DmsgServers) == 0 {
		return nil, nil, fmt.Errorf("no DMSG servers configured")
	}

	serverPtrs := make([]*disc.Entry, len(dmsg.Prod.DmsgServers))
	for i := range dmsg.Prod.DmsgServers {
		serverPtrs[i] = &dmsg.Prod.DmsgServers[i]
	}

	return StartDmsgDirectWithServers(ctx, dlog, pk, sk, dmsgDiscAddr, serverPtrs, dmsgSessions, destination)
}

// StartDmsgDirectWithServers starts a DMSG client using the provided set of DMSG servers.
// It attempts to connect and validate discovery access via the full server set.
func StartDmsgDirectWithServers(ctx context.Context, dlog *logging.Logger, pk cipher.PubKey, sk cipher.SecKey, dmsgDiscAddr string, dmsgServers []*disc.Entry, dmsgSessions int, destination string) (dmsgC *dmsg.Client, stop func(), err error) {

	if len(dmsgServers) == 0 {
		return nil, nil, fmt.Errorf("no DMSG servers provided")
	}

	// Fix `dmsg error 102 - entry is not of client in discovery` error
	destinationPk := cipher.PubKey{}
	if err = destinationPk.UnmarshalText([]byte(destination)); err != nil {
		return nil, nil, fmt.Errorf("destination address (pk) is wrong")
	}

	// Build direct client with all provided servers
	var keys cipher.PubKeys
	keys = append(keys, pk)
	entries := direct.GetAllEntries(keys, dmsgServers)
	dClient := direct.NewClient(entries, dlog)

	// Post client entry with all delegated servers
	var delegatedServers []cipher.PubKey
	for _, srv := range dmsgServers {
		delegatedServers = append(delegatedServers, srv.Static)
	}
	clientEntry := &disc.Entry{
		Client: &disc.Client{
			DelegatedServers: delegatedServers,
		},
		Static: destinationPk,
	}
	if err := dClient.PostEntry(ctx, clientEntry); err != nil {
		return nil, nil, fmt.Errorf("failed to post client entry: %w", err)
	}

	// Configure and start DMSG client
	dmsgConfig := dmsg.DefaultConfig()
	dmsgConfig.MinSessions = dmsgSessions

	dmsgC, stop, err = direct.StartDmsg(ctx, dlog, pk, sk, dClient, dmsgConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start DMSG client: %w", err)
	}
	if dmsgDiscAddr != "" {
		// Validate that we can access discovery over DMSG
		// Retry with exponential backoff to handle session initialization timing
		dmsgHTTP := &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}
		var resp *http.Response
		maxRetries := 5
		for i := 0; i < maxRetries; i++ {
			resp, err = dmsgHTTP.Get(dmsgDiscAddr + "/health")
			if err == nil {
				resp.Body.Close() //nolint
				break
			}
			if i < maxRetries-1 {
				backoff := time.Duration(200*(i+1)) * time.Millisecond
				dlog.WithError(err).Debugf("Failed to reach discovery, retrying in %v (attempt %d/%d)", backoff, i+1, maxRetries)
				time.Sleep(backoff)
			}
		}
		if err != nil {
			stop() // Cleanup if validation fails
			return nil, nil, fmt.Errorf("failed to reach discovery server via DMSG: %w", err)
		}
	}

	return dmsgC, stop, nil
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
	discPK := dmsg.ExtractPKFromDmsgAddr(flags.DmsgDiscAddr)
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
	httpDiscClient := disc.NewHTTP(flags.DmsgDiscURL, &http.Client{}, dlog)

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
	var lastErr error
	for _, client := range f.clients {
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
