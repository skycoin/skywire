// Package commands cmd/dmsgweb/commands/dmsgweb.go
package commands

import (
	"context"
	"net/http"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	dmsgcmdutil "github.com/skycoin/skywire/pkg/dmsg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/dmsgweb"
	"github.com/skycoin/skywire/pkg/logging"
)

const dwenv = "DMSGWEB"

var dwcfg = os.Getenv(dwenv)

func init() {
	webPort = cmdutil.SkyenvUintSlice("${WEBPORT[@]:-8080}", dwcfg)
	proxyPort = cmdutil.SkyenvUint("${PROXYPORT:-4445}", dwcfg)
	addProxy = cmdutil.SkyenvString("${ADDPROXY}", dwcfg)
	resolveDmsgAddr = cmdutil.SkyenvStringSlice("${RESOLVEPK[@]}", dwcfg)
	if os.Getenv("DMSGWEBSK") != "" {
		sk.Set(os.Getenv("DMSGWEBSK")) //nolint
	}
	if cmdutil.SkyenvString("${DMSGWEBSK}", dwcfg) != "" {
		sk.Set(cmdutil.SkyenvString("${DMSGWEBSK}", dwcfg)) //nolint
	}
	pk, _ = sk.PubKey() //nolint

	dmsgclient.InitFlags(RootCmd)
	RootCmd.Flags().StringVarP(&filterDomainSuffix, "filter", "f", ".dmsg", "domain suffix to filter\033[0m\n\r")
	RootCmd.Flags().UintVarP(&proxyPort, "socks", "q", proxyPort, "port to serve the socks5 proxy\033[0m\n\r")
	RootCmd.Flags().StringVarP(&addProxy, "addproxy", "r", addProxy, "configure additional socks5 proxy for dmsgweb (i.e. 127.0.0.1:1080)\033[0m\n\r")
	RootCmd.Flags().UintSliceVarP(&webPort, "port", "p", webPort, "port(s) to serve the web application\033[0m\n\r")
	RootCmd.Flags().StringSliceVarP(&resolveDmsgAddr, "resolve", "t", resolveDmsgAddr, "resolve the specified dmsg address:port on the local port as a raw TCP tunnel & disable proxy\033[0m\n\r")
	RootCmd.Flags().StringVarP(&proxyAddr, "proxy", "x", "", "connect to DMSG via proxy (i.e. '127.0.0.1:1080')\033[0m\n\r")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "debug", "[ debug | warn | error | fatal | panic | trace | info ]\033[0m\n\r")
	RootCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
	RootCmd.Flags().BoolVarP(&isEnvs, "envs", "E", false, "show example .conf file\033[0m\n\r")
	RootCmd.Flags().StringVar(&pprofMode, "pprofmode", "", "[ cpu | mem | mutex | block | trace | http ]")
	RootCmd.Flags().StringVar(&pprofAddr, "pprofaddr", "localhost:6060", "pprof http port")

}

// RootCmd contains the root command for dmsgweb
var RootCmd = &cobra.Command{
	Use:   dmsgclient.ExecName(),
	Short: "DMSG resolving proxy & browser client",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐┬ ┬┌─┐┌┐
	 │││││└─┐│ ┬│││├┤ ├┴┐
	─┴┘┴ ┴└─┘└─┘└┴┘└─┘└─┘
DMSG resolving proxy & browser client - access websites, HTTP & TCP interfaces over DMSG` + func() string {
		if _, err := os.Stat(dwcfg); err == nil {
			return `
dmsgweb conf file detected: ` + dwcfg
		}
		return `
.conf file may also be specified with
` + dwenv + `=/path/to/dmsgweb.conf skywire dmsg web`
	}(),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	PreRun: func(_ *cobra.Command, _ []string) {
		if isEnvs {
			printEnvs(envfileLinux)
		}
		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}
		dlog = logging.MustGetLogger("dmsgweb")

		err = dmsgclient.InitConfig()
		if err != nil {
			dlog.WithError(err).Fatal("Failed to read specified dmsghttp-config")
		}

		if dmsgclient.DmsgDiscURL == "" {
			dlog.Fatal("Dmsg Discovery Server URL not specified")
		}
		if dmsgclient.DmsgDiscAddr == "" {
			dlog.Fatal("Dmsg Discovery Server dmsg address not specified")
		}

		if len(resolveDmsgAddr) > 0 && len(webPort) != len(resolveDmsgAddr) {
			dlog.Fatal("--resolve -t flag cannot contain a different number of elements than --port -p flag")
		}
		if len(resolveDmsgAddr) == 0 && len(webPort) > 1 {
			dlog.Fatal("--port -p flag cannot specify multiple ports without specifying multiple dmsg address:port(s) to --resolve -t flag")
		}

		seenResolveDmsgAddr := make(map[string]bool)
		for _, item := range resolveDmsgAddr {
			if seenResolveDmsgAddr[item] {
				dlog.Fatal("--resolve -t flag cannot contain duplicates")
			}
			seenResolveDmsgAddr[item] = true
		}

		seenWebPort := make(map[uint]bool)
		for _, item := range webPort {
			if seenWebPort[item] {
				dlog.Fatal("--port -p flag cannot contain duplicates")
			}
			seenWebPort[item] = true
		}

		if len(webPort) == 0 {
			dlog.Fatal("webPort is empty. Ensure at least one port is specified.")
		}
		if filterDomainSuffix == "" {
			dlog.Fatal("domain suffix to filter cannot be an empty string")
		}
	},
	Run: func(_ *cobra.Command, _ []string) {
		stopPProf := dmsgcmdutil.InitPProf(dlog, pprofMode, pprofAddr)
		defer stopPProf()

		ctx, cancel := cmdutil.SignalContext(context.Background(), dlog)
		defer cancel()

		pk, err = sk.PubKey()
		if err != nil {
			pk, sk = cipher.GenerateKeyPair()
		}
		dlog.Info("dmsg client pk: ", pk.String())

		// Parse the --resolve flag entries into typed DMSG targets.
		// The runtime package owns parsing so its consumers (CLI and
		// future visor app) agree on the address format.
		targets := make([]dmsgweb.DmsgTarget, 0, len(resolveDmsgAddr))
		for _, raw := range resolveDmsgAddr {
			t, parseErr := dmsgweb.ParseDmsgTarget(raw)
			if parseErr != nil {
				dlog.WithError(parseErr).Fatal("invalid --resolve entry")
			}
			targets = append(targets, t)
		}

		// Set up the outer HTTP client used to bootstrap dmsg itself
		// (mirrors previous behavior — if --proxy is set the dmsg
		// bootstrap fetches go through the upstream SOCKS5 too).
		var outerHTTP *http.Client
		if proxyAddr != "" {
			d, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
			if err != nil {
				dlog.WithError(err).Fatal("Error creating SOCKS5 dialer")
			}
			outerHTTP = &http.Client{Transport: &http.Transport{Dial: d.Dial}}
		} else {
			outerHTTP = &http.Client{}
		}

		dmsgC, closeDmsg, err := dmsgclient.InitDmsgWithFlags(ctx, dlog, pk, sk, outerHTTP, "")
		if err != nil {
			dlog.WithError(err).Error("Error connecting to dmsg network")
			return
		}
		defer closeDmsg()

		cfg := dmsgweb.Config{
			DomainSuffix:  filterDomainSuffix,
			WebPorts:      webPort,
			ProxyPort:     proxyPort,
			ResolveAddr:   targets,
			UpstreamSOCKS: addProxy,
		}
		if err := dmsgweb.Run(ctx, dlog, dmsgC, cfg); err != nil && err != context.Canceled {
			dlog.WithError(err).Error("dmsgweb runtime stopped")
		}
	},
}

const envfileLinux = //nolint unused
`
#########################################################################
#--	DMSG WEB CONFIG TEMPLATE
#--		Defaults shown
#--		Uncomment to change default value
#--		WEBPORT and DMSGPORT must contain the same number of elements
#########################################################################

#--	Set port for proxy interface
#PROXYPORT=4445

#--	Configure additional socks5 proxy for dmsgweb to use to connect to dmsg
#ADDPROXY='127.0.0.1:1080'

#--	Web Interface Port
#WEBPORT=('8080')

#--	Resove a specific PK to the web port (also disables proxy)
#RESOLVEPK=('')

#--	Use raw tcp mode instead of http (also disables proxy)
#RAWTCP=('false')

#--	Dmsg port to use
#DMSGPORT=('80')

#--	Set secret key
#DMSGWEBSK=''
`
