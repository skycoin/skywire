// Package clirpc root.go
package clirpc

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
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

// Client is used by other skywire-cli commands to query the visor rpc.
// Supported address schemes:
//   - "localhost:3435" (default) — direct TCP to local visor
//   - "tp://<pk>" via --rpc flag — proxy through local visor's transport to remote visor
//   - VisorPK via --visor flag — connect over DMSG
func Client(cmdFlags *pflag.FlagSet) (visor.API, error) {
	// If VisorPK is provided, use dmsg connection
	if VisorPK != "" {
		return DmsgClient(cmdFlags)
	}

	// Check for tp:// scheme — transport proxy through local visor
	if strings.HasPrefix(Addr, "tp://") {
		internal.PrintError(cmdFlags, fmt.Errorf(
			"tp:// scheme detected. Use 'skywire cli visor tp-rpc %s <method>' instead.\n"+
				"Example: skywire cli visor tp-rpc %s Overview",
			Addr[5:], Addr[5:]))
		return nil, fmt.Errorf("tp:// not supported as --rpc address; use 'visor tp-rpc' command")
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

// isDmsgURL reports whether the given URL is a dmsg:// scheme URL that
// the visor's DmsgHTTP RPC can handle directly.
func isDmsgURL(u string) bool {
	return len(u) >= 7 && u[:7] == "dmsg://"
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

// fetchViaDmsgDirect creates ephemeral DMSG direct clients — one per DMSG server —
// and uses a FallbackRoundTripper to try each until one reaches the target service.
// This matches the pattern used by `skywire dmsg curl -B`: services connect to DMSG
// servers directly (not via discovery), so we must connect to each server to find them.
func fetchViaDmsgDirect(dmsgURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// Keep the ephemeral direct-client loggers quiet. They otherwise
	// dump session-open / yamux / EOF / entry-delete chatter at debug
	// level for every fetch, which drowns the caller's own output.
	// Use a dedicated master logger so we don't mutate the global one.
	dmaster := logging.NewMasterLogger()
	dmaster.SetLevel(logrus.ErrorLevel)
	dlog := dmaster.PackageLogger("cli:dmsg-fetch")
	pk, sk := cipher.GenerateKeyPair()

	servers := dmsg.Prod.DmsgServers
	if len(servers) == 0 {
		return nil, fmt.Errorf("no DMSG servers configured")
	}

	// Connect to each DMSG server via a separate direct client (same as dmsg curl -B)
	var dmsgClients []*dmsg.Client
	destination := dmsg.ExtractPKFromDmsgAddr(dmsgURL)
	for _, server := range servers {
		dmsgDC, closeFn, err := dmsgclient.StartDmsgDirectWithServers(ctx, dlog, pk, sk, "", []*disc.Entry{&server}, 1, destination)
		if err != nil {
			dlog.WithError(err).Debug("Failed to start direct client for server, skipping...")
			continue
		}
		dmsgClients = append(dmsgClients, dmsgDC)
		defer closeFn()
	}

	if len(dmsgClients) == 0 {
		return nil, fmt.Errorf("failed to connect to any DMSG servers")
	}

	// FallbackRoundTripper tries each client until one succeeds
	client := &http.Client{
		Transport: dmsgclient.NewFallbackRoundTripper(ctx, dmsgClients),
		Timeout:   30 * time.Second,
	}

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

// FetchCachedServiceURL is a drop-in replacement for cliutil.GetData that
// routes the fetch through FetchServiceURL (RPC → DMSG → HTTP) instead of
// going straight to HTTP.
//
// Caching semantics match GetData:
//   - If cachefile == "", caching is disabled and we fetch fresh every call.
//   - If the cache file exists and is younger than cacheFilesAge minutes,
//     its contents are returned as-is.
//   - Otherwise we fetch via FetchServiceURL and, on success, atomically
//     write the response to cachefile for the next call.
//   - On fetch failure, a stale cache file (if present) is returned as a
//     last resort rather than propagating an empty string, matching the
//     "best-effort" behavior callers expect.
//
// Returns the response body as a string, or "" if every path failed and
// there was no cache to fall back on. Errors are logged at debug level.
func FetchCachedServiceURL(cmdFlags *pflag.FlagSet, cachefile, thisurl string, cacheFilesAge int) string {
	// Serve a fresh cache if we have one.
	if cachefile != "" {
		if st, err := os.Stat(cachefile); err == nil {
			if time.Since(st.ModTime()).Minutes() <= float64(cacheFilesAge) {
				if data, err := os.ReadFile(cachefile); err == nil { //nolint:gosec
					return string(data)
				}
			}
		}
	}

	// Fetch via the RPC → DMSG → HTTP chain.
	body, err := FetchServiceURL(cmdFlags, thisurl)
	if err != nil {
		logger.Debugf("FetchCachedServiceURL: all fetch paths failed for %s: %v", thisurl, err)
		// Last-ditch: return stale cache if we have one.
		if cachefile != "" {
			if data, rerr := os.ReadFile(cachefile); rerr == nil { //nolint:gosec
				return string(data)
			}
		}
		return ""
	}

	// Write-through to cache for next call. Non-fatal on error.
	// Use world-readable permissions so multiple users (visor service,
	// CLI user) can share the same /tmp cache directory.
	if cachefile != "" {
		dir := filepath.Dir(cachefile)
		if err := os.MkdirAll(dir, 0o777); err != nil { //nolint:gosec
			logger.Debugf("FetchCachedServiceURL: cache mkdir failed for %s: %v", dir, err)
		}
		if werr := os.WriteFile(cachefile, body, 0o666); werr != nil { //nolint:gosec
			logger.Debugf("FetchCachedServiceURL: cache write failed for %s: %v", cachefile, werr)
		}
	}
	return string(body)
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

	// The visor's DmsgHTTP RPC parses req.Host as a dmsg.Addr (PK:port),
	// so hostname-based http:// URLs like http://dmsgd.skywire.skycoin.com
	// cannot be handled — they always fail with "invalid host address".
	// Resolve the http URL to its dmsg:// equivalent via the deployment
	// map before calling RPC; if no mapping exists we skip step 1 and
	// rely on step 2/3.
	rpcURL := url
	if dmsgEquiv := dmsgURLForHTTP(url); dmsgEquiv != "" {
		rpcURL = dmsgEquiv
	}

	// Step 1: Try visor RPC (proxies over DMSG via DmsgHTTP)
	if !NoRPC && (rpcURL != url || isDmsgURL(url)) {
		rpcClient, err := Client(cmdFlags)
		if err == nil {
			resp, err := rpcClient.DmsgHTTP(visor.DmsgHTTPRequest{
				URL:    rpcURL,
				Method: "GET",
			})
			if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return resp.Body, nil
			}
			if err != nil {
				lastErr = fmt.Errorf("RPC DmsgHTTP: %w", err)
				logger.Debugf("RPC DmsgHTTP failed for %s: %v", rpcURL, err)
			} else {
				lastErr = fmt.Errorf("RPC DmsgHTTP: status %d", resp.StatusCode)
				logger.Debugf("RPC DmsgHTTP status %d for %s", resp.StatusCode, rpcURL)
			}
		} else {
			lastErr = fmt.Errorf("RPC: %w", err)
			logger.Debugf("RPC not available: %v", err)
		}
	} else if !NoRPC {
		logger.Debugf("Skipping RPC step: no DMSG mapping for %s", url)
	}

	// Step 2: Direct DMSG HTTP (ephemeral client).
	// Only used when visor RPC is explicitly disabled (--no-rpc).
	// Step 1 (visor RPC) already handles DMSG access through the visor's
	// existing sessions. Step 2 creates an ephemeral DMSG client which
	// does discovery lookups that hang for services with server entries.
	if !NoDmsg && NoRPC {
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
