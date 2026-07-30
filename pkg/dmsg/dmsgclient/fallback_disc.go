// Package dmsgclient pkg/dmsg/dmsgclient/fallback_disc.go c1-net-dmsg
//
// The fallbackDiscClient is net/http-FREE (it only composes other
// disc.APIClient values), so it lives here untagged — usable from both the
// native (dmsghttp-backed) and TinyGo (dmsg-stream-backed) discovery-upgrade
// paths. The net/http-using bootstrap helpers (StartDmsgWith*,
// FallbackRoundTripper, cachingDiscClient) stay in cli_fallback.go behind
// //go:build !tinygo.
package dmsgclient

import (
	"context"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

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

// PostEntry ALWAYS no-ops via the direct client — even in the registering
// variant. The dmsg serve loop calls PostEntry PRE-session
// (initilizeClientEntry, "Initial post entry") BEFORE any session exists;
// routing that to HTTP-over-dmsg would dial dmsg-disc over the client's own
// not-yet-established sessions, blocking the serve loop on a chicken-egg and
// starving the rest of the server-dialing loop — the visor gets stuck on a
// single server / wedges. Durable registration is done by the POST-session
// updateClientEntry loop via PutEntry (runs only after sessions exist); the
// dmsg-disc setEntry handler upserts, so PutEntry creates the entry. Mirrors
// StartDmsgSelfHostedDisc, whose single client works precisely because its
// PostEntry no-ops and returns instantly so sessions can establish.
func (f *fallbackDiscClient) PostEntry(ctx context.Context, entry *disc.Entry) error {
	// EXCEPTION (registering variant): a never-before-registered client publishes its
	// FRESH entry via PostEntry, not PutEntry — updateClientEntryOnEndpoint does a
	// read-modify-write and, on "entry not found", registers a sequence-0 entry with
	// PostEntry; the PutEntry UPDATE path is only reached once an entry already
	// exists. So if the registering variant no-ops ALL PostEntry, a fresh client
	// (e.g. every ephemeral-key browser tab) never appears in discovery, loops at
	// 404 forever, and route setup can't even dial the source's own @136. Route to
	// HTTP — but ONLY once the entry carries delegated servers, i.e. POST-session.
	// The pre-session "Initial post entry" (initilizeClientEntry, before any session
	// is dialed) has an EMPTY server set; keeping that a no-op preserves the
	// chicken-egg guard (a PostEntry-over-dmsg with no sessions wedges the serve
	// loop — see the comment below). A real dmsg-disc upserts on this POST, so it
	// creates the entry that the later PutEntry updates keep alive.
	if f.register && entry.Client != nil && len(entry.Client.DelegatedServers) > 0 {
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

// AvailableServers delegates to the direct client. This is the BOOT / normal
// session-maintenance path — it must stay fast and never block on the live
// discovery (which isn't reachable until dmsg is up), so it returns the seed set
// the client already holds.
func (f *fallbackDiscClient) AvailableServers(ctx context.Context) ([]*disc.Entry, error) {
	return f.direct.AvailableServers(ctx)
}

// AllServers prefers the LIVE discovery (every registered server), falling back
// to the direct client's seed set only on failure/empty. This is the "maximize
// connectivity now" path (ConnectToAllServers) — not boot — so a live fetch is
// safe and desired. Delegating solely to `direct` pinned a seeded client (the
// wasm/browser edge, which boots from just 2 seed servers) to those seeds, so it
// never learned the rest of the deployment and couldn't rendezvous with peers
// delegated to other servers ("dmsg error 202 - cannot connect to delegated
// server") — "connect to all servers" was a no-op. The discovery's own entry is
// seed-cached, so this dial doesn't recurse.
func (f *fallbackDiscClient) AllServers(ctx context.Context) ([]*disc.Entry, error) {
	if srv, err := f.http.AllServers(ctx); err == nil && len(srv) > 0 {
		return srv, nil
	}
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
