// Package clirpc root.go
package clirpc

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/clicache"
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
	// Via, when non-empty, requests proxy to a remote visor through
	// the local visor named by --rpc. Supported schemes:
	//   - "dmsg://<pk>" — local visor opens a dmsg stream to <pk>:44
	//     and bridges bytes back to this CLI (multiplexed onto the
	//     existing --rpc port via cmux + magic-byte prefix).
	//   - "skynet://<pk>" — local visor proxies via TransportRPCCall
	//     (route ID 0 / VisorRPCPacket).
	Via string
)

// dmsgBridgeMagic must match the visor's pkg/visor/dmsg_bridge.go
// const of the same name. Kept as a literal here to avoid a CLI
// import of the visor package's internal constants.
const dmsgBridgeMagic = "SKYBRI"

// getDefaultRPCAddr returns the RPC address from environment variable or the default value
func getDefaultRPCAddr() string {
	if addr := os.Getenv(RPCAddrEnvVar); addr != "" {
		return addr
	}
	return skyenv.RPCAddr
}

// Client is used by other skywire-cli commands to query a visor RPC.
// The --rpc flag supports several schemes:
//
//   - "host:port" (default "localhost:3435") — direct TCP to the
//     local visor.
//   - "skynet://<pk>" — route the call to a REMOTE visor over a
//     skywire transport (stcpr/sudph). The CLI dials the local
//     visor as usual; every typed visor.API method gets transparently
//     proxied through the local visor's TransportRPCCall. The local
//     visor's PK must be in the remote visor's hypervisor or
//     dmsgpty whitelist. Transport is auto-created (stcpr→sudph
//     fallback) if one doesn't exist yet.
//   - "dmsg://<pk>" — direct dmsg dial to the remote visor's RPC
//     server. Currently NOT implemented (requires a server-side
//     listener that doesn't exist yet); returns an explicit error.
//
// On dial failure, prints the error to stderr via internal.PrintError
// so one-shot callers see the cause. Long-running reconnect loops
// (group listen, skychat listen) should use ClientQuiet to suppress
// per-retry spam.
func Client(cmdFlags *pflag.FlagSet) (visor.API, error) {
	return clientImpl(cmdFlags, false)
}

// ClientQuiet is the silent variant of Client. On dial failure it
// returns the error WITHOUT printing to stderr. Use this from any
// callsite that retries on failure (a noisy loop spamming the same
// "RPC connection failed" line is worse than no message at all —
// the caller knows the dial failed because err is non-nil).
func ClientQuiet(cmdFlags *pflag.FlagSet) (visor.API, error) {
	return clientImpl(cmdFlags, true)
}

func clientImpl(cmdFlags *pflag.FlagSet, quiet bool) (visor.API, error) {
	// --via takes precedence over --rpc scheme shortcuts. Schemes
	// on --rpc are kept as aliases for backward compat (a brief
	// transition; remove in a later release).
	via := Via
	switch {
	case via != "":
		// fall through to via dispatch below
	case strings.HasPrefix(Addr, "dmsg://"):
		via = Addr
		Addr = DefaultRPCAddr
	case strings.HasPrefix(Addr, "skynet://"), strings.HasPrefix(Addr, "tp://"):
		via = strings.Replace(Addr, "tp://", "skynet://", 1)
		Addr = DefaultRPCAddr
	}

	// VisorPK (set by old --visor flag path) routes the same as --via dmsg://
	if VisorPK != "" && via == "" {
		via = "dmsg://" + VisorPK
	}

	if via != "" {
		return viaClient(cmdFlags, via, quiet)
	}

	// Default: TCP connection to local RPC
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", Addr, rpcDialTimeout)
	if err != nil {
		if !quiet {
			internal.PrintError(cmdFlags, fmt.Errorf("RPC connection failed; is skywire running?: %v", err))
		}
		return nil, err
	}
	// Timeout of 0 means unlimited
	rpcCallTimeout := time.Duration(Timeout) * time.Second
	// Use logger with RPC address as tag for better identification
	rpcLogger := logging.MustGetLogger(fmt.Sprintf("rpc://%s", Addr))
	return visor.NewRPCClient(rpcLogger, conn, visor.RPCPrefix, rpcCallTimeout), nil
}

// viaClient dispatches a `--via <scheme>://<pk>` request to the
// shared bridge helper with the appropriate scheme byte.
//
// Both dmsg and skynet schemes ride the SAME byte-bridge mechanism
// (the visor's rpc_bridge.go selects which stream type to open
// based on the scheme byte we send). The CLI's rpc.Client speaks
// straight gob to the remote's rpc.Server through the bridge —
// no protocol translation, no JSON-vs-gob wire format issues, all
// 167 typed visor.API methods work transparently.
func viaClient(cmdFlags *pflag.FlagSet, via string, quiet bool) (visor.API, error) {
	var rest string
	var scheme byte
	switch {
	case strings.HasPrefix(via, "dmsg://"):
		rest = strings.TrimPrefix(via, "dmsg://")
		scheme = 0 // bridgeSchemeDmsg
	case strings.HasPrefix(via, "skynet://"):
		rest = strings.TrimPrefix(via, "skynet://")
		scheme = 1 // bridgeSchemeSkynet
	default:
		if !quiet {
			internal.PrintError(cmdFlags, fmt.Errorf(
				"--via must be dmsg://<pk>[:<port>] or skynet://<pk>; got %q", via))
		}
		return nil, fmt.Errorf("unsupported --via scheme: %q", via)
	}

	// Split <pk>[:<port>]. Port is optional; defaults to
	// DmsgVisorRPCPort. For skynet the port byte in the bridge
	// header is ignored (VStream has no port concept on route ID 0),
	// but we still parse a trailing port for shape consistency —
	// an operator passing skynet://<pk>:<port> gets the port
	// silently discarded by the visor bridge.
	pkStr := rest
	port := skyenv.DmsgVisorRPCPort
	if idx := strings.Index(rest, ":"); idx >= 0 {
		pkStr = rest[:idx]
		p, err := strconv.ParseUint(rest[idx+1:], 10, 16)
		if err != nil {
			if !quiet {
				internal.PrintError(cmdFlags, fmt.Errorf("invalid port in --via: %w", err))
			}
			return nil, err
		}
		port = uint16(p)
	}
	return bridgeClient(cmdFlags, scheme, pkStr, port, quiet)
}

// bridgeClient dials the local visor's CLI RPC port (default
// localhost:3435), sends the bridge magic + header (with the
// scheme byte selecting dmsg or skynet), and returns an
// rpc.Client running over the bridged conn. The visor opens the
// underlying stream using its own identity — no separate CLI
// keypair needed.
func bridgeClient(cmdFlags *pflag.FlagSet, scheme byte, pkStr string, port uint16, quiet bool) (visor.API, error) {
	var remotePK cipher.PubKey
	if err := remotePK.UnmarshalText([]byte(pkStr)); err != nil {
		if !quiet {
			internal.PrintError(cmdFlags, fmt.Errorf("invalid PK after scheme: %w", err))
		}
		return nil, err
	}

	localAddr := Addr
	if localAddr == "" || strings.Contains(localAddr, "://") {
		localAddr = DefaultRPCAddr
	}
	const dialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", localAddr, dialTimeout)
	if err != nil {
		if !quiet {
			internal.PrintError(cmdFlags, fmt.Errorf(
				"--via needs the local visor RPC at %s; dial failed: %w",
				localAddr, err))
		}
		return nil, err
	}

	// Header: 6 magic + 1 scheme + 33 PK + 2 LE port = 42 bytes.
	// cmux on the visor side matches the magic prefix and routes
	// the conn to the bridge handler; handler reads the remaining
	// 36 bytes and opens the appropriate stream type.
	header := make([]byte, 6+1+33+2)
	copy(header[:6], dmsgBridgeMagic)
	header[6] = scheme
	copy(header[7:40], remotePK[:])
	binary.LittleEndian.PutUint16(header[40:42], port)
	if _, err := conn.Write(header); err != nil {
		conn.Close() //nolint:errcheck,gosec
		if !quiet {
			internal.PrintError(cmdFlags, fmt.Errorf("bridge: write header: %w", err))
		}
		return nil, err
	}

	rpcCallTimeout := time.Duration(Timeout) * time.Second
	schemeName := "dmsg"
	if scheme == 1 {
		schemeName = "skynet"
	}
	rpcLogger := logging.MustGetLogger(fmt.Sprintf("%s://%s", schemeName, pkStr))
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

	// Parse CLI secret key. The standalone dmsg client needs a
	// keypair distinct from the local visor's — dmsg-discovery
	// rejects duplicate PK registrations, so reusing the visor's
	// SK from /opt/skywire/skywire.json on the same host hits a
	// PK-already-registered EOF before any stream can open.
	//
	// In practice this means operators have to add a dedicated
	// CLI keypair to the remote visor's dmsgpty whitelist and
	// pass --cli <sk> here. The future bridge architecture (CLI
	// borrows the local visor's dmsg client via an RPC proxy
	// method) will lift this requirement; until then the explicit
	// --cli flag is the auth path.
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
		logger.Infof("Using --cli key: %s", cliPK)
	} else {
		cliPK, cliSK = cipher.GenerateKeyPair()
		logger.Warnf("No --cli key provided, using ephemeral key (rejected by any whitelisted listener): %s", cliPK)
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

	// Dial visor over dmsg at the visor-RPC port. Previously this
	// dialed DmsgHypervisorPort 46 which is wrong-direction (visors
	// DIAL hypervisors there; the hypervisor side runs rpc.Client,
	// not rpc.Server). DmsgVisorRPCPort 44 is the correct port —
	// visor runs rpc.Server.ServeConn on accepted streams there,
	// gated by the same hypervisor + dmsgpty whitelist as
	// TransportRPCServer.
	addr := dmsg.Addr{PK: visorPubKey, Port: skyenv.DmsgVisorRPCPort}
	logger.Infof("Dialing visor %s over dmsg on port %d...", visorPubKey, skyenv.DmsgVisorRPCPort)

	conn, err := dmsgC.Dial(ctx, addr)
	if err != nil {
		internal.PrintError(cmdFlags, fmt.Errorf("failed to dial visor over dmsg: %v", err))
		return nil, err
	}

	logger.Info("Connected to visor over dmsg")
	// Use the configured timeout for RPC calls
	rpcCallTimeout := time.Duration(Timeout) * time.Second
	// Use logger with dmsg address as tag for better identification
	dmsgLogger := logging.MustGetLogger(fmt.Sprintf("dmsg://%s", VisorPK))
	return visor.NewRPCClient(dmsgLogger, conn, visor.RPCPrefix, rpcCallTimeout), nil
}

var (
	// NoCXO disables the CXO subscriber step in FetchServiceURL.
	NoCXO bool
	// NoRPC disables the RPC step in FetchServiceURL.
	NoRPC bool
	// NoDmsg disables the direct DMSG HTTP step in FetchServiceURL.
	NoDmsg bool
	// NoHTTP disables the HTTP fallback step in FetchServiceURL.
	NoHTTP bool
)

// RegisterFetchFlags adds --no-cxo, --no-rpc, --no-dmsg, and --no-http
// flags to a command. Call this in init() for any command that uses
// FetchServiceURL.
func RegisterFetchFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&NoCXO, "no-cxo", false, "skip CXO subscriber-cache step")
	cmd.Flags().BoolVar(&NoRPC, "no-rpc", false, "skip visor RPC (DmsgHTTP) step")
	cmd.Flags().BoolVar(&NoDmsg, "no-dmsg", false, "skip direct DMSG HTTP step")
	cmd.Flags().BoolVar(&NoHTTP, "no-http", false, "skip direct HTTP fallback step")
}

// isDmsgURL reports whether the given URL is a dmsg:// scheme URL that
// the visor's DmsgHTTP RPC can handle directly.
func isDmsgURL(u string) bool {
	return len(u) >= 7 && u[:7] == "dmsg://"
}

// DmsgURLForHTTP looks up the DMSG equivalent URL for an HTTP service URL.
// Returns empty string if no DMSG address is known for the service.
//
// Exported so callers outside cmd/skywire-cli/commands/rpc (notably
// cli log) can rewrite a service URL to its dmsg-form before fetching
// over a caller-owned dmsg.Client, avoiding a plain-HTTP hop through
// the deployment-services HTTP edge.
func DmsgURLForHTTP(httpURL string) string {
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

// cxoFeedForURL maps a deployment-service HTTP/DMSG URL to the CXO
// (feed, path) pair the visor's lazy-on-demand subscriber publishes
// for it. Returns ok=false when no CXO mirror is configured for the
// URL — the caller falls through to the existing RPC/DMSG/HTTP chain.
//
// Adding a new feed: register a publisher on the service side, a
// matching subscriber on the visor side, and add a row here. The
// CLI fetch chain picks it up automatically.
//
// Standalone uptime tracker (deployment.Prod.UptimeTracker /
// UptimeTrackerDmsg) is intentionally absent — the service is being
// deprecated; mirroring it over CXO would just be sunk effort.
func cxoFeedForURL(rawURL string) (feed, path string, ok bool) {
	// TPD /metrics → "tpd-metrics" feed, "metrics/days/<n>" path.
	// Only the windows the publisher actually writes are CXO-eligible
	// — see uptimePublishDays / metricsPublishDays in TPD. Anything
	// else falls through to the network chain.
	if isUnderBase(rawURL, deployment.Prod.TransportDiscovery, "/metrics") ||
		isUnderBase(rawURL, deployment.Prod.TransportDiscoveryDmsg, "/metrics") {
		days := queryParamInt(rawURL, "days", -1)
		if days == 1 || days == 7 || days == 30 {
			return "tpd-metrics", fmt.Sprintf("metrics/days/%d", days), true
		}
		return "", "", false
	}

	// TPD /uptimes → "tpd-uptime" feed. The publisher writes per-day
	// windows; the CLI's graph commands hit /uptimes?v=v3 without an
	// explicit days param so we read the 30d bucket which always
	// contains today's data and trims older days client-side.
	if isUnderBase(rawURL, deployment.Prod.TransportDiscovery, "/uptimes") ||
		isUnderBase(rawURL, deployment.Prod.TransportDiscoveryDmsg, "/uptimes") {
		// Only v3 carries timeline bitmaps — v1/v2 callers don't
		// gain anything from CXO over the existing chain since the
		// payload is small.
		if v := queryParam(rawURL, "v"); v == "v3" {
			return "tpd-uptime", "uptimes/days/30", true
		}
		return "", "", false
	}

	// SD /api/services?type=X → "sd-services" feed. The visor's
	// CXOSubscriptionManager already maintains a materialized
	// FeedSDServices snapshot for TabCLIServices and TabAutoconnect;
	// the FetchCXO branch just walks it for the requested type.
	if isUnderBase(rawURL, deployment.Prod.ServiceDiscovery, "/api/services") ||
		isUnderBase(rawURL, deployment.Prod.ServiceDiscoveryDmsg, "/api/services") {
		if t := queryParam(rawURL, "type"); t != "" {
			return "sd-services", "type/" + t, true
		}
		return "", "", false
	}

	// TPD /all-transports → "tpd-all-transports" feed. The publisher
	// emits two variants (with-self / without-self) on a 60s
	// cadence; the CLI's `pv -t`, `tp tree`, and `tp viz` callers
	// hit /all-transports without an explicit selfTransports flag,
	// which the TPD endpoint serves as "without self".
	if isUnderBase(rawURL, deployment.Prod.TransportDiscovery, "/all-transports") ||
		isUnderBase(rawURL, deployment.Prod.TransportDiscoveryDmsg, "/all-transports") {
		return "tpd-all-transports", "without-self", true
	}

	return "", "", false
}

// isUnderBase reports whether rawURL begins with `base + suffix`.
// Empty bases never match — keeps deployment configs without a DMSG
// equivalent from accidentally aliasing onto every URL.
func isUnderBase(rawURL, base, suffix string) bool {
	if base == "" {
		return false
	}
	target := base + suffix
	return len(rawURL) >= len(target) && rawURL[:len(target)] == target
}

// queryParam extracts a single query-string value by name. Returns
// "" when the URL has no query, when the key is absent, or when
// parsing fails. (Stdlib's url.Parse handles these edges; we just
// pick out the value.)
func queryParam(rawURL, name string) string {
	idx := -1
	for i := 0; i < len(rawURL); i++ {
		if rawURL[i] == '?' {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ""
	}
	q := rawURL[idx+1:]
	for _, kv := range splitOn(q, '&') {
		eq := -1
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				eq = i
				break
			}
		}
		if eq < 0 {
			continue
		}
		if kv[:eq] == name {
			return kv[eq+1:]
		}
	}
	return ""
}

// queryParamInt is queryParam parsed as an int with a fallback when
// missing or unparseable.
func queryParamInt(rawURL, name string, fallback int) int {
	v := queryParam(rawURL, name)
	if v == "" {
		return fallback
	}
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return fallback
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func splitOn(s string, sep byte) []string {
	out := []string{}
	last := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			out = append(out, s[last:i])
			last = i + 1
		}
	}
	out = append(out, s[last:])
	return out
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
// Caching semantics:
//   - If cachefile == "", caching is disabled and we fetch fresh every call.
//     Otherwise the cachefile argument is treated as a non-empty marker for
//     "caching enabled"; the actual storage is a single bbolt DB at
//     pkg/clicache's DefaultPath (NOT the per-URL JSON file the legacy
//     code used). The bbolt approach replaces the older /tmp/<host>/<endpoint>.json
//     files which couldn't be shared across users due to umask downgrade.
//   - If a cached entry exists and is younger than cacheFilesAge minutes,
//     its body is returned as-is.
//   - Otherwise we fetch via FetchServiceURL and write the response through
//     to the bbolt cache for the next call.
//   - On fetch failure, a stale cached entry is returned as a last resort
//     rather than propagating an empty string.
//
// Returns the response body as a string, or "" if every path failed and
// there was no cache to fall back on. Errors are logged at debug level.
func FetchCachedServiceURL(cmdFlags *pflag.FlagSet, cachefile, thisurl string, cacheFilesAge int) string {
	cache := getCLICacheIfEnabled(cachefile)
	maxAge := time.Duration(cacheFilesAge) * time.Minute

	// Serve a fresh cache entry if we have one.
	if cache != nil {
		if e, ok := cache.Fresh(thisurl, maxAge); ok {
			return string(e.Body)
		}
	}

	// Fetch via the RPC → DMSG → HTTP chain.
	body, err := FetchServiceURL(cmdFlags, thisurl)
	if err != nil {
		logger.Debugf("FetchCachedServiceURL: all fetch paths failed for %s: %v", thisurl, err)
		// Last-ditch: return stale cache if we have one.
		if cache != nil {
			if e, ok := cache.Get(thisurl); ok {
				return string(e.Body)
			}
		}
		return ""
	}

	// Write-through to cache for next call. Non-fatal on error.
	if cache != nil {
		if werr := cache.Put(thisurl, body); werr != nil {
			logger.Debugf("FetchCachedServiceURL: cache put failed for %s: %v", thisurl, werr)
		}
	}
	return string(body)
}

var (
	cliCacheOnce sync.Once
	cliCache     *clicache.Cache
)

// getCLICacheIfEnabled returns the process-singleton bbolt cache iff
// cachefile is non-empty (the historical "caching on" signal). The
// argument's path is no longer used — the DB location is resolved
// via clicache.DefaultPath. Returns nil when caching is disabled or
// when the DB couldn't be opened (e.g. another CLI holds the lock);
// callers treat nil as "no cache, fetch fresh every call".
func getCLICacheIfEnabled(cachefile string) *clicache.Cache {
	if cachefile == "" {
		return nil
	}
	cliCacheOnce.Do(func() {
		path, err := clicache.DefaultPath()
		if err != nil {
			logger.Debugf("clicache: resolve default path: %v", err)
			return
		}
		c, err := clicache.Open(path)
		if err != nil {
			logger.Debugf("clicache: open %s: %v", path, err)
			return
		}
		cliCache = c
	})
	return cliCache
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

	// Step 0: CXO subscriber cache. When the URL maps to a feed the
	// visor already subscribes to, the cached payload is a local
	// memory read — no DMSG round-trip, no service round-trip. Falls
	// through silently on miss; the visor's lazy-on-demand
	// connect-with-cooldown keeps repeated probes from costing more
	// than the first one when the publisher is down.
	if !NoCXO {
		if feed, path, ok := cxoFeedForURL(url); ok {
			if rpcClient, err := Client(cmdFlags); err == nil {
				resp, err := rpcClient.FetchCXO(visor.FetchCXOArgs{Feed: feed, Path: path})
				if err == nil && resp != nil && resp.Hit && len(resp.Body) > 0 {
					logger.Debugf("CXO hit for %s (feed=%s path=%s, root@%s)",
						url, feed, path, resp.LastRootAt.Format(time.RFC3339))
					return resp.Body, nil
				}
				if err != nil {
					logger.Debugf("CXO probe error for %s: %v", url, err)
				} else if resp != nil {
					logger.Debugf("CXO miss for %s: %s", url, resp.Reason)
				}
			} else {
				logger.Debugf("CXO step skipped (no RPC client): %v", err)
			}
		}
	}

	// The visor's DmsgHTTP RPC parses req.Host as a dmsg.Addr (PK:port),
	// so hostname-based http:// URLs like http://dmsgd.skywire.skycoin.com
	// cannot be handled — they always fail with "invalid host address".
	// Resolve the http URL to its dmsg:// equivalent via the deployment
	// map before calling RPC; if no mapping exists we skip step 1 and
	// rely on step 2/3.
	rpcURL := url
	if dmsgEquiv := DmsgURLForHTTP(url); dmsgEquiv != "" {
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
		dmsgURL := DmsgURLForHTTP(url)
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
