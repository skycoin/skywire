// Package rewardsserver pkg/deployment/rewards/server/cmd.go c3-app-rewards
package rewardsserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/0magnet/calvin"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/tpviz"
)

var err error
var (
	startTime           = time.Now()
	sk                  cipher.SecKey
	dmsgDisc            string
	dmsgPort            uint16
	dmsgSess            int
	wl                  string
	wd                  string
	wlkeys              []cipher.PubKey
	webPort             uint
	webBind             string
	loginNode           string
	loginNodeAddr       string // resolved URL of login chain node (set at runtime)
	loginGenesisAddress string // genesis wallet address for login verification (set at runtime)
	skycoinNode         string
	loginChainFlags     string
	healthOnly          bool
	canonicalDomain     string
	log                 = logging.MustGetLogger("rewards")

	// disableTpVizAPI prevents tp-viz from mounting /api/* routes that
	// could conflict with the visor's hypervisor API when the reward
	// system is hosted by the visor.
	disableTpVizAPI bool

	// tpvizSrv is the tp-viz server built by buildRouter, kept here so
	// serveStandalone can hand it the ONE dmsg client this process owns once
	// that client exists. buildRouter runs first and has no client to give.
	tpvizSrv *tpviz.Server
)

var skyenvfile = os.Getenv("SKYENV")

func init() {
	ServerCmd.CompletionOptions.DisableDefaultCmd = true
	defaultWebPort := scriptExecUint("${WEBPORT:-80}")
	ServerCmd.Flags().UintVarP(&webPort, "port", "p", defaultWebPort, "port to serve (deprecated: use --bind)")
	ServerCmd.Flags().StringVar(&webBind, "bind", fmt.Sprintf(":%d", defaultWebPort), "listen address host:port to serve on\n(empty host = all interfaces; 127.0.0.1:80 = localhost only)")
	ServerCmd.Flags().Uint16VarP(&dmsgPort, "dport", "d", scriptExecUint16("${DMSGPORT:-80}"), "dmsg port to serve")
	ServerCmd.Flags().IntVarP(&dmsgSess, "dsess", "e", scriptExecInt("${DMSGSESSIONS:-1}"), "dmsg sessions")
	msg := "add whitelist keys, comma separated to permit POST of reward transaction to be broadcast"
	if scriptExecArray("${REWARDPKS[@]}") != "" {
		msg += "\n\r"
	}
	ServerCmd.Flags().StringVarP(&wl, "wl", "w", scriptExecArray("${REWARDPKS[@]}"), msg)
	wd, err = os.Getwd()
	if err != nil {
		// Only a flag default. A js/wasm instance started by Go's stock
		// wasm_exec.js has no working directory (process.cwd throws ENOSYS),
		// and this init runs in every such instance of the root binary — the
		// wallet cipher, the tpviz view — so it must not be fatal there.
		wd = "."
	}
	ServerCmd.Flags().StringVarP(&wd, "wd", "W", wd, "location of dir containing 'log_collection' & reward 'hist' dirs")
	LoginChainCmd.Flags().StringVarP(&wd, "wd", "W", wd, "working directory for login chain files (login_genesis.json, login_fiber.toml)")
	ServerCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "D", deployment.Prod.DmsgDiscovery, "dmsg discovery url")
	if os.Getenv("DMSGHTTP_SK") != "" {
		sk.Set(os.Getenv("DMSGHTTP_SK")) //nolint:errcheck,gosec
	}
	if scriptExecString("${DMSGHTTP_SK}") != "" {
		sk.Set(scriptExecString("${DMSGHTTP_SK}")) //nolint:errcheck,gosec
	}
	ServerCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
	ServerCmd.Flags().StringVar(&loginNode, "login-node", scriptExecString("${LOGINNODE}"), "login chain node: empty=disabled, 'auto'=auto-setup on localhost:6421,\nor URL of external node (e.g. http://localhost:6421)")
	ServerCmd.Flags().StringVar(&skycoinNode, "skycoin-node", scriptExecString("${SKYCOINNODE:-http://127.0.0.1:6420}"), "Skycoin mainnet node URL for reward transaction broadcasts")
	ServerCmd.Flags().StringVar(&loginChainFlags, "login-chain-flags", scriptExecString("${LOGINCHAIN_FLAGS}"), "override flags for login chain skycoin daemon subprocess\n(default: --block-publisher --localhost-only --download-peerlist=false\n--disable-default-peers --disable-csrf --host-whitelist=fiber.skywire.dev)")
	ServerCmd.Flags().BoolVar(&healthOnly, "health-only", false, "serve only /health endpoint for testing")
	ServerCmd.Flags().StringVar(&surveyPushMinVersion, "survey-min-version", scriptExecString("${SURVEY_MIN_VERSION}"), "min skywire version a PUSHed survey may report to be stored/eligible\n(empty = accept any parseable version; the getlogs.sh prune over log_backups\nremains the authoritative floor). Set to the current reward floor to give\nvisors an accurate eligible/ineligible signal for the hypervisor UI.")
	ServerCmd.Flags().StringVar(&canonicalDomain, "canonical", scriptExecString("${CANONICAL:-https://haltingstate.net}"), "canonical domain for SEO, a host that serves these pages directly (e.g. https://haltingstate.net)")
}

// ServerCmd starts the reward system ui server
var ServerCmd = &cobra.Command{
	Use:   "ui",
	Short: "reward system UI server",
	Long: "skycoin reward system user interface server and skywire network metrics:\n https://reward.skywire.dev\n" + calvin.AsciiFont("fiber") + func() string {
		if _, err := os.Stat(skyenvfile); err == nil {
			return `run the web application

skyenv file detected: ` + skyenvfile
		}
		return `run the web application

.conf file may also be specified with
SKYENV=/path/to/fiber.conf fiber run`
	}(),
	Run: func(cmd *cobra.Command, _ []string) {
		r := buildRouter()
		serveStandalone(r, resolveBindAddr(cmd, webBind, webPort))
	},
}

// resolveBindAddr computes the effective listen address for the server.
// --bind wins if set. Otherwise, if the deprecated --port was set, its value
// is mapped into the host part of the default --bind address (preserving the
// all-interfaces-vs-localhost semantics of the default). If neither was
// changed, the default --bind value is used.
func resolveBindAddr(cmd *cobra.Command, bind string, port uint) string {
	if cmd.Flags().Changed("bind") {
		return bind
	}
	if cmd.Flags().Changed("port") {
		host, _, err := net.SplitHostPort(bind)
		if err != nil {
			host = ""
		}
		return net.JoinHostPort(host, strconv.FormatUint(uint64(port), 10))
	}
	return bind
}

// RewardConfig holds configuration for the reward system when hosted
// by the visor. The visor constructs this from its config and calls
// ConfigureAndBuild to get the HTTP handler.
type RewardConfig struct {
	WorkDir         string
	WhitelistPKs    []cipher.PubKey
	CanonicalDomain string
	SkycoinNode     string
	LoginNode       string
	HealthOnly      bool
	DisableTpVizAPI bool // prevent tp-viz from mounting /api/* routes
	// SecKey, DmsgPort and SurveyMinVersion are for Serve.
	SecKey           cipher.SecKey
	DmsgPort         uint16
	SurveyMinVersion string
}

// ConfigureAndBuild sets the reward system's internal state from the
// given config and builds the HTTP handler. This is the entry point
// for visor integration — the visor calls this instead of running
// the CLI command.
func ConfigureAndBuild(cfg RewardConfig) http.Handler {
	if cfg.WorkDir != "" {
		wd = cfg.WorkDir
	}
	if len(cfg.WhitelistPKs) > 0 {
		wlkeys = cfg.WhitelistPKs
	}
	if cfg.CanonicalDomain != "" {
		canonicalDomain = cfg.CanonicalDomain
	}
	if cfg.SkycoinNode != "" {
		skycoinNode = cfg.SkycoinNode
	}
	if cfg.LoginNode != "" {
		loginNode = cfg.LoginNode
	}
	healthOnly = cfg.HealthOnly
	disableTpVizAPI = cfg.DisableTpVizAPI
	startTime = time.Now()
	return buildRouter()
}

// Serve builds the server from cfg and runs it under cfg.SecKey until ctx
// ends. An empty bind serves dmsg only.
func Serve(ctx context.Context, cfg RewardConfig, bind string, l *logging.Logger) error {
	if cfg.SecKey.Null() {
		return errors.New("the reward service needs a secret key")
	}
	sk = cfg.SecKey
	if cfg.DmsgPort != 0 {
		dmsgPort = cfg.DmsgPort
	}
	if cfg.SurveyMinVersion != "" {
		surveyPushMinVersion = cfg.SurveyMinVersion
	}
	return serve(ctx, ConfigureAndBuild(cfg), bind, l)
}

// The SKYENV file is parsed in Go, never sourced by a shell: these run in
// init for every skywire command, and a file that runs skywire would recurse.
func scriptExecString(s string) string { return cmdutil.SkyenvString(s, skyenvfile) }
func scriptExecArray(s string) string  { return cmdutil.SkyenvArray(s, skyenvfile) }
func scriptExecInt(s string) int       { return cmdutil.SkyenvInt(s, skyenvfile) }
func scriptExecUint(s string) uint     { return cmdutil.SkyenvUint(s, skyenvfile) }
func scriptExecUint16(s string) uint16 { return uint16(cmdutil.SkyenvUint(s, skyenvfile)) } //nolint:gosec
