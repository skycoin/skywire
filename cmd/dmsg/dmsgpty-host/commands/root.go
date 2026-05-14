// Package commands cmd/dmsgpty-host/commands/root.go
package commands

import (
	"context"
	"fmt"
	stdlog "log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	jsoniter "github.com/json-iterator/go"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	dmsgcmdutil "github.com/skycoin/skywire/pkg/dmsg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgpty"
	"github.com/skycoin/skywire/pkg/logging"
)

const defaultEnvPrefix = "DMSGPTY"

var log = logging.MustGetLogger("dmsgpty-host:init")

var json = jsoniter.ConfigFastest

// variables
var (
	// persistent flags
	dmsgDisc     = dmsg.DiscAddr(false)
	dmsgSessions = dmsg.DefaultMinSessions
	dmsgPort     = dmsgpty.DefaultPort
	cliNet       = dmsgpty.DefaultCLINet
	cliAddr      = dmsgpty.DefaultCLIAddr()
	sk           cipher.SecKey
	pk           cipher.PubKey
	wl           cipher.PubKeys

	// tcpListen, when non-empty, brings up the direct-TCP entry point
	// (XK noise + dmsgpty whitelist gate) alongside the dmsg-overlay
	// path. Mirrors the visor-embedded Dmsgpty.SshListen config.
	tcpListen string

	// noDmsg, when true, skips the dmsg.Client + dmsg-listener setup
	// entirely. Use with --tcplisten (or --listen-fd) to run the host
	// as an sshd-style direct-TCP-only daemon. Without --no-dmsg the
	// host runs both the dmsg-overlay path and the optional TCP path
	// in parallel, which is the right shape for visor-embedded use
	// but the wrong shape for a standalone "ssh-replacement" daemon
	// that would otherwise contend with the operator's visor for the
	// dmsg-discovery entry for the same PK.
	noDmsg bool

	// skFromVisor, when non-empty, loads the SK (and derives the PK)
	// from the named skywire-config.json's top-level `sk` field
	// instead of from --sk / config / env. Lets the standalone host
	// share the visor's identity (only safe in --no-dmsg mode — two
	// dmsg.Clients with the same PK racing in dmsg discovery is the
	// failure mode this avoids).
	skFromVisor string

	// listenFD, when >= 0, switches the host into single-conn
	// socket-activation mode: read the inherited file descriptor as a
	// net.Conn, run one noise + authorize + session pipeline against
	// it, exit when the session closes. The companion systemd unit
	// shape is `dmsgpty-tcp.socket` (Accept=yes) + a templated
	// `dmsgpty-tcp@.service` that runs this binary with --listen-fd
	// 0 — one process per session, master listener restartable
	// without affecting running sessions. Mirrors the sshd-via-
	// systemd-socket-activation pattern.
	listenFD = -1

	// persistent flags
	envPrefix = defaultEnvPrefix

	// root command flags
	confStdin = false
	confPath  = "./config.json"
	pprofMode string
	pprofAddr string
)

// init prepares flags.
func init() {

	// Prepare flags with env/config references.
	RootCmd.Flags().Var(&wl, "wl", "whitelist of the dmsgpty-host")
	RootCmd.Flags().StringVar(&dmsgDisc, "dmsgdisc", dmsgDisc, "dmsg discovery address")
	RootCmd.Flags().IntVar(&dmsgSessions, "dmsgsessions", dmsgSessions, "minimum number of dmsg sessions to ensure")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgport", dmsgPort, "dmsg port for listening for remote hosts")
	RootCmd.Flags().StringVar(&cliNet, "clinet", cliNet, "network used for listening for cli connections")
	RootCmd.Flags().StringVar(&cliAddr, "cliaddr", cliAddr, "address used for listening for cli connections")
	RootCmd.Flags().StringVar(&tcpListen, "tcplisten", "",
		"optional direct-TCP entry point address (e.g. ':2022'); empty disables. XK-noise + whitelist gated, mirrors the visor-embedded Dmsgpty.SshListen. Exposed as 'skywire cli sshd'.")
	RootCmd.Flags().BoolVar(&noDmsg, "no-dmsg", false,
		"skip dmsg.Client + dmsg listener; run as TCP-only daemon (use with --tcplisten or --listen-fd)")
	RootCmd.Flags().StringVar(&skFromVisor, "sk-from-visor", "",
		"load SK from the named skywire-config.json's .sk field; intended for --no-dmsg + visor-PK shared identity")
	RootCmd.Flags().IntVar(&listenFD, "listen-fd", -1,
		"single-conn socket-activation mode: read inherited FD as net.Conn, run one session, exit (companion systemd .socket Accept=yes)")
	// Prepare flags without associated env/config references.
	RootCmd.Flags().StringVar(&envPrefix, "envprefix", envPrefix, "env prefix")
	RootCmd.Flags().BoolVar(&confStdin, "confstdin", confStdin, "config will be read from stdin if set")
	RootCmd.Flags().StringVarP(&confPath, "confpath", "c", confPath, "config path")
	RootCmd.Flags().StringVar(&pprofMode, "pprofmode", "", "[ cpu | mem | mutex | block | trace | http ]")
	RootCmd.Flags().StringVar(&pprofAddr, "pprofaddr", "localhost:6060", "pprof http port")

}

// RootCmd contains commands for dmsgpty-host
var RootCmd = &cobra.Command{
	Use:   dmsgclient.ExecName(),
	Short: "DMSG host for pseudoterminal command line interface",
	Long: calvin.AsciiFont("dmsgpty-host") + `
	DMSG host for pseudoterminal (pty) command line interface`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		// --sk-from-visor takes precedence over flag/env/config-file
		// SKs by running before getConfig validates the SK. We refuse
		// the combination with dmsg-on, because two dmsg.Clients with
		// the same PK racing for one dmsg-discovery entry is the
		// failure mode this flag is designed to avoid.
		if skFromVisor != "" {
			if !noDmsg {
				return fmt.Errorf("--sk-from-visor requires --no-dmsg (sharing PK with a running visor's dmsg client breaks the visor's dmsg-disc entry)")
			}
			if err := loadSKFromVisorConfig(skFromVisor); err != nil {
				return err
			}
		}

		conf, err := getConfig(cmd, false)
		if err != nil {
			return fmt.Errorf("failed to get config: %w", err)
		}

		if _, err := buildinfo.Get().WriteTo(stdlog.Writer()); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}
		log := logging.MustGetLogger("dmsgpty-host")
		stopPProf := dmsgcmdutil.InitPProf(log, pprofMode, pprofAddr)
		defer stopPProf()
		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		pk, err := sk.PubKey()
		if err != nil {
			return fmt.Errorf("failed to derive public key from secret key: %w", err)
		}

		// Prepare whitelist (needed regardless of dmsg/tcp/socket-activation mode).
		wl, err := dmsgpty.NewConfigWhitelist(confPath)
		if err != nil {
			return fmt.Errorf("failed to init whitelist: %w", err)
		}

		// dmsg.Client is optional. --no-dmsg skips the client entirely,
		// for the sshd-style standalone-TCP daemon shape; without it
		// we'd be racing the visor (if any) for the same dmsg-disc
		// entry under the shared PK that --sk-from-visor pulls in.
		var dmsgC *dmsg.Client
		if !noDmsg {
			dmsgC = dmsg.NewClient(pk, sk, disc.NewHTTP(conf.DmsgDisc, &http.Client{}, log), &dmsg.Config{
				MinSessions: conf.DmsgSessions,
			})
			defer func() {
				if err := dmsgC.Close(); err != nil {
					log.WithError(err).Warn("Failed to close dmsg client")
				}
			}()

			go dmsgC.Serve(ctx)
			select {
			case <-ctx.Done():
				return fmt.Errorf("failed to wait dmsg client to be ready: %w", ctx.Err())
			case <-dmsgC.Ready():
			}
		}

		// Prepare dmsgpty host. dmsgC may be nil in --no-dmsg mode;
		// the TCP entry points are explicitly nil-safe (see host_tcp.go).
		host := dmsgpty.NewHost(dmsgC, wl)

		// Socket-activation single-conn mode: read inherited FD,
		// wrap as net.Conn, run one session pipeline, exit. The unit
		// shape behind this is `Accept=yes` on the .socket so each
		// session gets its own process — `systemctl restart
		// dmsgpty-tcp.socket` only affects the listener, not running
		// sessions, matching sshd's fork-per-session resilience.
		if listenFD >= 0 {
			f := os.NewFile(uintptr(listenFD), fmt.Sprintf("listen-fd-%d", listenFD))
			if f == nil {
				return fmt.Errorf("listen-fd %d is not a valid file descriptor", listenFD)
			}
			lc, err := net.FileConn(f)
			if err != nil {
				_ = f.Close() //nolint:errcheck
				return fmt.Errorf("listen-fd %d net.FileConn: %w", listenFD, err)
			}
			// FileConn dup'd; the original FD can be closed.
			_ = f.Close() //nolint:errcheck
			log.WithField("listen_fd", listenFD).Info("dmsgpty-host: socket-activation mode, serving one session.")
			host.ServeAcceptedTCP(ctx, lc, pk, sk)
			return nil
		}

		wg := new(sync.WaitGroup)

		// CLI listener — local Unix socket for `dmsgpty-cli` control
		// of the whitelist + session start. Available in all modes;
		// dmsg-less hosts can still admin their whitelist via CLI.
		if conf.CLINet == "unix" {
			_ = os.Remove(conf.CLIAddr) //nolint:errcheck
		}
		cliL, err := net.Listen(conf.CLINet, conf.CLIAddr)
		if err != nil {
			return fmt.Errorf("failed to serve CLI: %w", err)
		}
		log.WithField("addr", cliL.Addr()).Info("Listening for CLI connections.")
		wg.Add(1)
		go func() {
			log.WithError(host.ServeCLI(ctx, cliL)).
				Info("Stopped serving CLI.")
			wg.Done()
		}()

		// Dmsg-overlay listener — only when dmsg is enabled.
		if dmsgC != nil {
			log.WithField("port", conf.DmsgPort).
				Info("Listening for dmsg streams.")
			wg.Add(1)
			go func() {
				log.WithError(host.ListenAndServe(ctx, conf.DmsgPort)).
					Info("Stopped serving dmsgpty-host.")
				wg.Done()
			}()
		}

		// Optional direct-TCP entry point (XK-noise + whitelist
		// gated). Mirrors the visor-embedded Dmsgpty.SshListen.
		// Empty (default) skips it; non-empty brings the listener up
		// alongside the dmsg-overlay path. The host's NewHost-wired wl
		// is reused — no separate ACL.
		tcpAddr := tcpListen
		if tcpAddr == "" {
			tcpAddr = conf.SshListen
		}
		if tcpAddr != "" {
			wg.Add(1)
			log.WithField("addr", tcpAddr).
				Info("Listening for direct-TCP streams (XK-noise gated).")
			go func() {
				log.WithError(host.ListenAndServeTCP(ctx, tcpAddr, pk, sk)).
					Info("Stopped serving dmsgpty-host TCP entry point.")
				wg.Done()
			}()
		}

		wg.Wait()
		return nil
	},
}

// Execute executes the root command.
func Execute() {
	dmsgclient.Execute(RootCmd)
}

func configFromJSON(conf dmsgpty.Config) (dmsgpty.Config, error) {
	var jsonConf dmsgpty.Config

	if confStdin {
		if err := json.NewDecoder(os.Stdin).Decode(&jsonConf); err != nil {
			return dmsgpty.Config{}, fmt.Errorf("flag 'confstdin' is set, but config read from stdin is invalid: %w", err)
		}
	}

	if confPath != "" {
		f, err := os.Open(confPath)
		if err != nil {
			return dmsgpty.Config{}, fmt.Errorf("failed to open config file: %w", err)
		}
		if err := json.NewDecoder(f).Decode(&jsonConf); err != nil {
			return dmsgpty.Config{}, fmt.Errorf("flag 'confpath' is set, but we failed to read config from specified path: %w", err)
		}
	}

	if jsonConf.SK != "" {
		if err := sk.Set(jsonConf.SK); err != nil {
			return dmsgpty.Config{}, fmt.Errorf("provided SK is invalid: %w", err)
		}
	}

	if !sk.Null() {
		conf.SK = jsonConf.SK
	}

	if jsonConf.PK != "" {
		if err := pk.Set(jsonConf.PK); err != nil {
			return dmsgpty.Config{}, fmt.Errorf("provided PK is invalid: %w", err)
		}
	}

	if !pk.Null() {
		conf.PK = jsonConf.PK
	}

	if len(jsonConf.WL) > 0 {
		ustString := strings.Join(jsonConf.WL, ",")
		if err := wl.Set(ustString); err != nil {
			return dmsgpty.Config{}, fmt.Errorf("provided WL's are invalid: %w", err)
		}
	}

	if len(wl) > 0 {
		conf.WL = jsonConf.WL
	}

	if jsonConf.DmsgDisc != "" {
		conf.DmsgDisc = jsonConf.DmsgDisc
	}

	if conf.DmsgSessions != 0 {
		conf.DmsgSessions = jsonConf.DmsgSessions
	}

	if conf.DmsgPort != 0 {
		conf.DmsgPort = jsonConf.DmsgPort
	}

	if conf.CLINet != "" {
		conf.CLINet = jsonConf.CLINet
	}

	if conf.CLIAddr != "" {
		conf.CLIAddr = dmsgpty.ParseWindowsEnv(jsonConf.CLIAddr)
	}

	if conf.CLIAddr == "" {
		conf.CLIAddr = dmsgpty.DefaultCLIAddr()
	}

	return conf, nil
}

func fillConfigFromENV(conf dmsgpty.Config) (dmsgpty.Config, error) {

	if val, ok := os.LookupEnv(envPrefix + "_DMSGDISC"); ok {
		conf.DmsgDisc = val
	}

	if val, ok := os.LookupEnv(envPrefix + "_DMSGSESSIONS"); ok {
		dmsgSessions, err := strconv.Atoi(val)
		if err != nil {
			return conf, fmt.Errorf("failed to parse dmsg sessions: %w", err)
		}

		conf.DmsgSessions = dmsgSessions
	}

	if val, ok := os.LookupEnv(envPrefix + "_DMSGPORT"); ok {
		dmsgPort, err := strconv.ParseUint(val, 10, 16)
		if err != nil {
			return conf, fmt.Errorf("failed to parse dmsg port: %w", err)
		}

		conf.DmsgPort = uint16(dmsgPort) //nolint
	}

	if val, ok := os.LookupEnv(envPrefix + "_CLINET"); ok {
		conf.CLINet = val
	}

	if val, ok := os.LookupEnv(envPrefix + "_CLIADDR"); ok {
		conf.CLIAddr = val
	}

	return conf, nil
}

func fillConfigFromFlags(conf dmsgpty.Config) dmsgpty.Config {
	if dmsgDisc != dmsg.DiscAddr(false) {
		conf.DmsgDisc = dmsgDisc
	}

	if dmsgSessions != dmsg.DefaultMinSessions {
		conf.DmsgSessions = dmsgSessions
	}

	if dmsgPort != dmsgpty.DefaultPort {
		conf.DmsgPort = dmsgPort
	}

	if cliNet != dmsgpty.DefaultCLINet {
		conf.CLINet = cliNet
	}

	if cliAddr != dmsgpty.DefaultCLIAddr() {
		conf.CLIAddr = cliAddr
	}

	return conf
}

// loadSKFromVisorConfig reads the top-level `sk` field from a skywire
// visor config and sets the package-level sk variable. Used by the
// --sk-from-visor flag so a standalone dmsgpty-host can share the
// visor's identity (only safe when --no-dmsg is also set; otherwise
// two dmsg.Clients race for the same dmsg-discovery entry under that
// PK).
//
// Returns an error if the file can't be read, the JSON is malformed,
// or the `sk` field is missing/invalid. On success sk is mutated in
// place — the caller is responsible for deriving pk from it afterward.
func loadSKFromVisorConfig(path string) error {
	f, err := os.Open(path) //nolint:gosec // operator-supplied path
	if err != nil {
		return fmt.Errorf("sk-from-visor open %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck
	var probe struct {
		SK string `json:"sk"`
	}
	if err := json.NewDecoder(f).Decode(&probe); err != nil {
		return fmt.Errorf("sk-from-visor decode %s: %w", path, err)
	}
	if probe.SK == "" {
		return fmt.Errorf("sk-from-visor %s: missing top-level 'sk' field", path)
	}
	if err := sk.Set(probe.SK); err != nil {
		return fmt.Errorf("sk-from-visor %s: invalid sk: %w", path, err)
	}
	return nil
}

// getConfig sources variables in the following precedence order: flags, env, config, default.
func getConfig(cmd *cobra.Command, skGen bool) (dmsgpty.Config, error) {
	conf := dmsgpty.DefaultConfig()

	var err error
	// Prepare how config file is sourced (if root command).
	if cmd.Name() == cmdutil.RootCmdName() {
		conf, err = configFromJSON(conf)
		if err != nil {
			return dmsgpty.Config{}, fmt.Errorf("failed to read config from JSON: %w", err)
		}
	}
	conf, err = fillConfigFromENV(conf)
	if err != nil {
		return conf, fmt.Errorf("failed to fill config from ENV: %w", err)
	}

	if skGen {
		pk, sk = cipher.GenerateKeyPair()
		log.WithField("pubkey", pk).
			WithField("seckey", sk).
			Info("Generating key pair as 'skgen' is set.")
		conf.SK = sk.Hex()
		conf.PK = pk.Hex()
	}
	conf = fillConfigFromFlags(conf)

	if sk.Null() {
		return conf, fmt.Errorf("value 'seckey' is invalid")
	}

	// Print values.
	pLog := logrus.FieldLogger(log)
	pLog = pLog.WithField("dmsgdisc", conf.DmsgDisc)
	pLog = pLog.WithField("dmsgsessions", conf.DmsgSessions)
	pLog = pLog.WithField("dmsgport", conf.DmsgPort)
	pLog = pLog.WithField("clinet", conf.CLINet)
	pLog = pLog.WithField("cliaddr", conf.CLIAddr)
	pLog = pLog.WithField("pk", conf.PK)
	pLog = pLog.WithField("wl", conf.WL)
	pLog.Info("Init complete.")

	return conf, nil
}
