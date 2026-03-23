// Package dmsgclient pkg/dmsgclient/cli.go
package dmsgclient

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/pkg/direct"
	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
	"github.com/skycoin/dmsg/pkg/ioutil"
)

// ExecName returns the name of the currently running executable,
// suitable for use as cobra.Command.Use.
func ExecName() string {
	return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
}

// Execute runs the given cobra command and exits on error.
func Execute(cmd *cobra.Command) {
	if err := cmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

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
	if UseDC {
		return StartDmsgDirect(ctx, dlog, pk, sk, "", DmsgSessions, dmsg.ExtractPKFromDmsgAddr(destination))
	}
	if UseHTTP {
		resp, err := httpClient.Get(DmsgDiscURL + "/health")
		if err != nil {
			dlog.WithError(err).Fatal("Error connecting to dmsg-discovery with http client")
		}
		defer ioutil.CloseQuietly(resp.Body, dlog)

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			dlog.WithError(err).Error("Failed to read response body from discovery")
		} else {
			dlog.Infof("Received response from dmsg-discovery server %s/health:\n%s", DmsgDiscURL, string(body))
		}

		// Use direct client with synthetic entries for discovery server and all dmsg servers
		// This allows dialing the discovery server which doesn't register itself
		return StartDmsgWithDirectClient(ctx, dlog, pk, sk, DmsgSessions)
	}

	// Default dmsghttp mode
	var dmsgHTTP *http.Client
	var dmsgClients []*dmsg.Client
	var closeFns []func()

	dlog.Debug("Starting DMSG direct clients.")
	for _, server := range dmsg.Prod.DmsgServers {
		if len(dmsgClients) >= DmsgSessions {
			break
		}

		dmsgDC, closeFn, err := StartDmsgDirectWithServers(ctx, dlog, pk, sk, DmsgDiscAddr, []*disc.Entry{&server}, DmsgSessions, dmsg.ExtractPKFromDmsgAddr(DmsgDiscAddr))
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
	resp, err := dmsgHTTP.Get(DmsgDiscAddr + "/health")
	if err != nil {
		for _, fn := range closeFns {
			fn()
		}
		dlog.WithError(err).Fatal("All DMSG transports failed to reach discovery /health")
	}
	defer ioutil.CloseQuietly(resp.Body, dlog)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		dlog.WithError(err).Error("Failed to read discovery /health response body")
	} else {
		dlog.Infof("Received response from dmsg-discovery server %s/health:\n%s", DmsgDiscAddr, string(body))
	}

	return StartDmsgWithSyntheticDiscovery(ctx, dlog, pk, sk, dmsgHTTP, DmsgDiscAddr, DmsgSessions)
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
