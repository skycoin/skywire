// Package cli internal/cli/go
package cli

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"

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

		return StartDmsg(ctx, dlog, pk, sk, httpClient, flags.DmsgDiscURL, flags.DmsgSessions)
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

	return StartDmsg(ctx, dlog, pk, sk, dmsgHTTP, flags.DmsgDiscAddr, flags.DmsgSessions)
}

// StartDmsg starts dmsg returns a dmsg client for the given dmsg discovery
func StartDmsg(ctx context.Context, dlog *logging.Logger, pk cipher.PubKey, sk cipher.SecKey, httpClient *http.Client, dmsgDisc string, dmsgSessions int) (dmsgC *dmsg.Client, stop func(), err error) {
	if dlog == nil {
		return nil, nil, fmt.Errorf("nil logger")
	}

	dmsgC = dmsg.NewClient(pk, sk, disc.NewHTTP(dmsgDisc, httpClient, dlog), &dmsg.Config{MinSessions: dmsgSessions})
	dlog.Debug("Created dmsg client.")

	go dmsgC.Serve(context.Background())
	dlog.Debug("dmsgclient.Serve(context.Background())")

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
		dmsgHTTP := &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}
		resp, err := dmsgHTTP.Get(dmsgDiscAddr + "/health")
		if err != nil {
			stop() // Cleanup if validation fails
			return nil, nil, fmt.Errorf("failed to reach discovery server via DMSG: %w", err)
		}
		resp.Body.Close() //nolint
	}

	return dmsgC, stop, nil
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
