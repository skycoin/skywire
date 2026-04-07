// Package clirpc root.go
package clirpc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor"
)

const (
	// RPCAddrEnvVar is the environment variable name for the RPC server address
	RPCAddrEnvVar = "SKYWIRE_RPC"
)

var (
	logger = logging.MustGetLogger("skywire-cli")
	// DefaultRPCAddr is the default RPC address (from env var or default value)
	DefaultRPCAddr = getDefaultRPCAddr()
	// Addr is the address (ip:port) of the rpc server
	Addr string
	// Timeout is the timeout for RPC calls in seconds (0 = unlimited)
	Timeout int = 30
	// VisorPK is the public key of the visor to connect to over dmsg
	VisorPK string
	// CliSK is the secret key for authenticating CLI over dmsg
	CliSK string
)

// getDefaultRPCAddr returns the RPC address from environment variable or the default value
func getDefaultRPCAddr() string {
	if addr := os.Getenv(RPCAddrEnvVar); addr != "" {
		return addr
	}
	return skyenv.RPCAddr
}

// Client is used by other skywire-cli commands to query the visor rpc
func Client(cmdFlags *pflag.FlagSet) (visor.API, error) {
	// If VisorPK is provided, use dmsg connection
	if VisorPK != "" {
		return DmsgClient(cmdFlags)
	}

	// Default: TCP connection to local RPC
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", Addr, rpcDialTimeout)
	if err != nil {
		internal.PrintError(cmdFlags, fmt.Errorf("RPC connection failed; is skywire running?: %v", err))
		return nil, err
	}
	// Timeout of 0 means unlimited
	rpcCallTimeout := time.Duration(Timeout) * time.Second
	// Use logger with RPC address as tag for better identification
	rpcLogger := logging.MustGetLogger(fmt.Sprintf("rpc://%s", Addr))
	return visor.NewRPCClient(rpcLogger, conn, visor.RPCPrefix, rpcCallTimeout), nil
}

// DmsgClient creates an RPC client over dmsg
func DmsgClient(cmdFlags *pflag.FlagSet) (visor.API, error) {
	// Parse visor public key
	visorPubKey := cipher.PubKey{}
	if err := visorPubKey.UnmarshalText([]byte(VisorPK)); err != nil {
		internal.PrintError(cmdFlags, fmt.Errorf("invalid visor public key: %v", err))
		return nil, err
	}

	// Parse CLI secret key (if provided)
	var cliPK cipher.PubKey
	var cliSK cipher.SecKey
	if CliSK != "" {
		if err := cliSK.UnmarshalText([]byte(CliSK)); err != nil {
			internal.PrintError(cmdFlags, fmt.Errorf("invalid CLI secret key: %v", err))
			return nil, err
		}
		var err error
		cliPK, err = cliSK.PubKey()
		if err != nil {
			internal.PrintError(cmdFlags, fmt.Errorf("failed to derive public key: %v", err))
			return nil, err
		}
		logger.Infof("Using CLI key: %s", cliPK)
	} else {
		// Generate ephemeral keypair if no secret key provided
		cliPK, cliSK = cipher.GenerateKeyPair()
		logger.Warnf("No --cli key provided, using ephemeral key (will fail if visor has whitelist): %s", cliPK)
	}

	// Create dmsg client
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	dmsgConf := &dmsg.Config{
		MinSessions:    1,
		UpdateInterval: time.Minute,
	}

	// Use production dmsg discovery
	dmsgDisc := disc.NewHTTP(deployment.Prod.DmsgDiscovery, &http.Client{}, logger)

	dmsgC := dmsg.NewClient(cliPK, cliSK, dmsgDisc, dmsgConf)
	go dmsgC.Serve(context.Background())

	// Wait for dmsg client to be ready
	select {
	case <-dmsgC.Ready():
		logger.Info("Dmsg client ready")
	case <-ctx.Done():
		internal.PrintError(cmdFlags, fmt.Errorf("dmsg client initialization timed out"))
		return nil, ctx.Err()
	}

	// Dial visor over dmsg
	addr := dmsg.Addr{PK: visorPubKey, Port: skyenv.DmsgHypervisorPort}
	logger.Infof("Dialing visor %s over dmsg on port %d...", visorPubKey, skyenv.DmsgHypervisorPort)

	conn, err := dmsgC.Dial(ctx, addr)
	if err != nil {
		internal.PrintError(cmdFlags, fmt.Errorf("failed to dial visor over dmsg: %v", err))
		return nil, err
	}

	logger.Info("Connected to visor over dmsg")
	// Use the configured timeout for RPC calls
	rpcCallTimeout := time.Duration(Timeout) * time.Second
	// Use logger with dmsg address as tag for better identification
	dmsgLogger := logging.MustGetLogger(fmt.Sprintf("dmsg://%s", VisorPK[:8]))
	return visor.NewRPCClient(dmsgLogger, conn, visor.RPCPrefix, rpcCallTimeout), nil
}

var (
	// NoRPC disables the RPC step in FetchServiceURL.
	NoRPC bool
	// NoDmsg disables the direct DMSG HTTP step in FetchServiceURL.
	NoDmsg bool
	// NoHTTP disables the HTTP fallback step in FetchServiceURL.
	NoHTTP bool
)

// RegisterFetchFlags adds --no-rpc, --no-dmsg, and --no-http flags to a command.
// Call this in init() for any command that uses FetchServiceURL.
func RegisterFetchFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&NoRPC, "no-rpc", false, "skip visor RPC (DmsgHTTP) step")
	cmd.Flags().BoolVar(&NoDmsg, "no-dmsg", false, "skip direct DMSG HTTP step")
	cmd.Flags().BoolVar(&NoHTTP, "no-http", false, "skip direct HTTP fallback step")
}

// dmsgURLForHTTP looks up the DMSG equivalent URL for an HTTP service URL.
// Returns empty string if no DMSG address is known for the service.
func dmsgURLForHTTP(httpURL string) string {
	// Map HTTP base URLs to their DMSG counterparts from the deployment config
	mappings := map[string]string{
		deployment.Prod.TransportDiscovery: deployment.Prod.TransportDiscoveryDmsg,
		deployment.Prod.DmsgDiscovery:      deployment.Prod.DmsgDiscoveryDmsg,
		deployment.Prod.AddressResolver:    deployment.Prod.AddressResolverDmsg,
		deployment.Prod.RouteFinder:        deployment.Prod.RouteFinderDmsg,
		deployment.Prod.UptimeTracker:      deployment.Prod.UptimeTrackerDmsg,
		deployment.Prod.ServiceDiscovery:   deployment.Prod.ServiceDiscoveryDmsg,
	}
	for httpBase, dmsgBase := range mappings {
		if httpBase != "" && dmsgBase != "" && len(httpURL) >= len(httpBase) && httpURL[:len(httpBase)] == httpBase {
			// Replace the HTTP base with the DMSG base, keep the path
			return dmsgBase + httpURL[len(httpBase):]
		}
	}
	return ""
}

// fetchViaDmsgDirect creates an ephemeral DMSG client, connects to the network,
// and fetches the given dmsg:// URL. This is heavyweight but works without a
// running visor, making it the right choice for DMSG-only deployments.
func fetchViaDmsgDirect(dmsgURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	log := logging.MustGetLogger("cli:dmsg-fetch")

	pk, sk := cipher.GenerateKeyPair()

	// Create discovery client
	discURL := deployment.Prod.DmsgDiscovery
	if discURL == "" {
		return nil, fmt.Errorf("no DMSG discovery URL configured")
	}
	httpClient := &http.Client{Timeout: 15 * time.Second}
	discClient := disc.NewHTTP(discURL, httpClient, log)

	// Create and start DMSG client
	dmsgConfig := dmsg.DefaultConfig()
	dmsgConfig.MinSessions = 1
	dmsgC := dmsg.NewClient(pk, sk, discClient, dmsgConfig)
	go dmsgC.Serve(ctx) //nolint:errcheck

	// Wait for ready
	select {
	case <-ctx.Done():
		dmsgC.Close() //nolint:errcheck
		return nil, ctx.Err()
	case <-dmsgC.Ready():
	case <-time.After(20 * time.Second):
		dmsgC.Close() //nolint:errcheck
		return nil, fmt.Errorf("timeout connecting to DMSG network")
	}
	defer dmsgC.Close() //nolint:errcheck

	// Use dmsghttp transport to make HTTP request over DMSG
	transport := &dmsgHTTPRoundTripper{ctx: ctx, dmsgC: dmsgC}
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second}

	resp, err := client.Get(dmsgURL) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("DMSG HTTP request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read DMSG response: %w", err)
	}

	if resp.StatusCode >= 300 {
		return body, fmt.Errorf("DMSG HTTP %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// dmsgHTTPRoundTripper implements http.RoundTripper using a dmsg client
type dmsgHTTPRoundTripper struct {
	ctx   context.Context
	dmsgC *dmsg.Client
}

func (t *dmsgHTTPRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var addr dmsg.Addr
	if err := addr.Set(req.Host); err != nil {
		return nil, fmt.Errorf("invalid DMSG address %q: %w", req.Host, err)
	}

	conn, err := t.dmsgC.Dial(t.ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("DMSG dial failed: %w", err)
	}

	// Write HTTP request over DMSG stream
	if err := req.Write(conn); err != nil {
		conn.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to write request: %w", err)
	}

	// Read HTTP response
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		conn.Close() //nolint:errcheck
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return resp, nil
}

// FetchServiceURL fetches a URL from a deployment service using a three-step chain:
//  1. RPC — ask the running visor to proxy the request over DMSG (DmsgHTTP RPC)
//  2. DMSG direct — create ephemeral DMSG client and fetch directly
//  3. HTTP — direct HTTP request as last resort
//
// Steps can be disabled via --no-rpc, --no-dmsg, and --no-http flags.
//
// This pattern ensures CLI commands work for:
//   - Visors running with DMSG (step 1)
//   - Standalone CLI without a running visor on DMSG network (step 2)
//   - Environments without DMSG connectivity (step 3)
func FetchServiceURL(cmdFlags *pflag.FlagSet, url string) ([]byte, error) {
	var lastErr error

	// Step 1: Try visor RPC (proxies over DMSG via DmsgHTTP)
	if !NoRPC {
		rpcClient, err := Client(cmdFlags)
		if err == nil {
			resp, err := rpcClient.DmsgHTTP(visor.DmsgHTTPRequest{
				URL:    url,
				Method: "GET",
			})
			if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return resp.Body, nil
			}
			if err != nil {
				lastErr = fmt.Errorf("RPC DmsgHTTP: %w", err)
				logger.Debugf("RPC DmsgHTTP failed for %s: %v", url, err)
			} else {
				lastErr = fmt.Errorf("RPC DmsgHTTP: status %d", resp.StatusCode)
				logger.Debugf("RPC DmsgHTTP status %d for %s", resp.StatusCode, url)
			}
		} else {
			lastErr = fmt.Errorf("RPC: %w", err)
			logger.Debugf("RPC not available: %v", err)
		}
	}

	// Step 2: Direct DMSG HTTP (ephemeral client)
	if !NoDmsg {
		dmsgURL := dmsgURLForHTTP(url)
		if dmsgURL != "" {
			logger.Debugf("Trying direct DMSG fetch: %s", dmsgURL)
			body, err := fetchViaDmsgDirect(dmsgURL)
			if err == nil {
				return body, nil
			}
			lastErr = fmt.Errorf("DMSG direct: %w", err)
			logger.Debugf("DMSG direct failed for %s: %v", dmsgURL, err)
		} else {
			logger.Debugf("No DMSG address known for %s, skipping DMSG step", url)
		}
	}

	// Step 3: Direct HTTP fallback
	if !NoHTTP {
		httpClient := &http.Client{Timeout: 30 * time.Second}
		resp, err := httpClient.Get(url) //nolint:gosec
		if err != nil {
			lastErr = fmt.Errorf("HTTP: %w", err)
			logger.Debugf("HTTP failed for %s: %v", url, err)
		} else {
			defer resp.Body.Close() //nolint:errcheck
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, fmt.Errorf("failed to read HTTP response: %w", err)
			}
			if resp.StatusCode < 300 {
				return body, nil
			}
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
			logger.Debugf("HTTP status %d for %s", resp.StatusCode, url)
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("all fetch methods disabled or unavailable for %s", url)
}
