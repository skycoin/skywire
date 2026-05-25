// Package cmdutil pkg/skywire-utilities/pkg/cmdutil/dmsg_bootstrap.go
package cmdutil

import (
	"context"
	"net/http"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
)

// DmsgBootstrap holds the result of bootstrapping a service's DMSG connection.
type DmsgBootstrap struct {
	Client  *dmsg.Client
	DClient disc.APIClient
	Close   func()
}

// BootstrapDmsg creates a DMSG client for a service using the bootstrap priority:
//  1. Embedded deployment config (dmsg.Prod/Test servers) — no network needed
//  2. HTTP discovery fallback if embedded servers are empty
//
// After connecting, a background goroutine refreshes the server list from
// discovery (preferring DMSG transport, falling back to HTTP).
func BootstrapDmsg(
	ctx context.Context,
	log *logging.Logger,
	pk cipher.PubKey,
	sk cipher.SecKey,
	embeddedServers []disc.Entry,
	dmsgDisc string,
	dmsgServerType string,
) (*DmsgBootstrap, error) {
	// Step 1: Use embedded servers for initial bootstrap
	var servers []*disc.Entry
	for i := range embeddedServers {
		servers = append(servers, &embeddedServers[i])
	}

	if len(servers) > 0 {
		log.Infof("Using %d embedded DMSG servers for bootstrap", len(servers))
	}

	// Step 2: If no embedded servers, fall back to HTTP discovery
	if len(servers) == 0 && dmsgDisc != "" {
		log.Info("No embedded DMSG servers, fetching from discovery via HTTP...")
		servers = dmsghttp.GetServers(ctx, dmsgDisc, dmsgServerType, log)
	}

	if len(servers) == 0 {
		log.Warn("No DMSG servers available for bootstrap")
	}

	// Filter by server type if specified
	if dmsgServerType != "" {
		var filtered []*disc.Entry
		for _, s := range servers {
			if s.Server != nil && s.Server.ServerType == dmsgServerType {
				filtered = append(filtered, s)
			}
		}
		if len(filtered) > 0 {
			servers = filtered
		}
	}

	// Create direct client pre-loaded with the bootstrap server entries.
	// Its only job is to serve AllServers / AvailableServers from memory
	// so the dmsg client can dial the embedded set without an HTTP round
	// trip — useful when the dmsg-discovery's HTTP endpoint is briefly
	// unreachable (offline install, DNS failure, Caddy outage).
	keys := cipher.PubKeys{pk}
	directDClient := direct.NewClient(direct.GetAllEntries(keys, servers), log)

	// Wrap the direct client with an HTTP-discovery fallback so per-PK
	// Entry() lookups query the real dmsg-discovery instead of the
	// in-memory bootstrap set. Without this, dmsg.DialStream returned
	// "dmsg error 100 - entry is not found in discovery" for every PK
	// not in the embedded server list — even though direct.Client never
	// actually contacts a discovery service. AllServers / PostEntry
	// stay on the direct client so the bootstrap phase is unchanged;
	// only the Entry() path now consults real discovery.
	//
	// When dmsgDisc is empty the caller has explicitly opted out of
	// HTTP discovery (e.g. fully air-gapped deployments) — keep the
	// direct-only behavior in that case.
	var dClient disc.APIClient = directDClient
	if dmsgDisc != "" {
		httpDiscClient := disc.NewHTTP(dmsgDisc, &http.Client{Timeout: 30 * time.Second}, log)
		dClient = dmsgclient.NewFallbackDiscClient(directDClient, httpDiscClient, log)
	}

	config := &dmsg.Config{
		MinSessions:          0, // 0 = connect to all available servers
		UpdateInterval:       dmsg.DefaultUpdateInterval,
		ConnectedServersType: dmsgServerType,
	}

	dmsgDC, closeDmsgDC, err := direct.StartDmsg(ctx, log, pk, sk, dClient, config)
	if err != nil {
		return nil, err
	}

	// Background: refresh servers — try DMSG first, fall back to HTTP.
	// Updates are PostEntry'd into the direct client so AllServers stays
	// current; the fallback wrapper delegates PostEntry to direct.
	go updateServersDmsgFirst(ctx, dClient, dmsgDC, dmsgDisc, dmsgServerType, log)

	return &DmsgBootstrap{
		Client:  dmsgDC,
		DClient: dClient,
		Close:   closeDmsgDC,
	}, nil
}

// updateServersDmsgFirst periodically refreshes the DMSG server list.
// It tries to reach discovery over DMSG first (private, no DNS dependency),
// falling back to HTTP if DMSG fails.
func updateServersDmsgFirst(
	ctx context.Context,
	dClient disc.APIClient,
	dmsgC *dmsg.Client,
	dmsgDisc string,
	dmsgServerType string,
	log *logging.Logger,
) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			servers := fetchServers(ctx, dmsgC, dmsgDisc, dmsgServerType, log)
			for _, server := range servers {
				dClient.PostEntry(ctx, server) //nolint:errcheck,gosec
				if err := dmsgC.EnsureSession(ctx, server); err != nil {
					log.WithField("remote_pk", server.Static).WithError(err).Warn("Failed to establish session")
				}
			}
		}
	}
}

// fetchServers tries to get servers over DMSG first, falls back to HTTP.
func fetchServers(
	ctx context.Context,
	dmsgC *dmsg.Client,
	dmsgDisc string,
	dmsgServerType string,
	log *logging.Logger,
) []*disc.Entry {
	// Try DMSG transport first
	if dmsgC != nil {
		dmsgHTTP := &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}
		dmsgDiscClient := disc.NewHTTP(dmsgDisc, dmsgHTTP, log)

		timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		servers, err := dmsgDiscClient.AllServers(timeoutCtx)
		cancel()

		if err == nil && len(servers) > 0 {
			log.Debugf("Refreshed %d servers via DMSG", len(servers))
			return filterByType(servers, dmsgServerType)
		}
		log.WithError(err).Debug("DMSG server refresh failed, trying HTTP")
	}

	// Fall back to HTTP
	httpClient := disc.NewHTTP(dmsgDisc, &http.Client{Timeout: 30 * time.Second}, log)

	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	servers, err := httpClient.AllServers(timeoutCtx)
	cancel()

	if err != nil {
		log.WithError(err).Error("HTTP server refresh also failed")
		return nil
	}

	log.Debugf("Refreshed %d servers via HTTP", len(servers))
	return filterByType(servers, dmsgServerType)
}

func filterByType(servers []*disc.Entry, serverType string) []*disc.Entry {
	if serverType == "" {
		return servers
	}
	var filtered []*disc.Entry
	for _, s := range servers {
		if s.Server != nil && s.Server.ServerType == serverType {
			filtered = append(filtered, s)
		}
	}
	if len(filtered) > 0 {
		return filtered
	}
	return servers
}
