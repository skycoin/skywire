// Package commands cmd/address-resolver/commands/root.go
package commands

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/skycoin/dmsg/pkg/direct"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/httpauth"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire-utilities/pkg/storeconfig"
	"github.com/skycoin/skywire-utilities/pkg/tcpproxy"
	"github.com/spf13/cobra"
	"github.com/xtaci/kcp-go"

	"github.com/skycoin/skywire-services/internal/armetrics"
	"github.com/skycoin/skywire-services/pkg/address-resolver/api"
	"github.com/skycoin/skywire-services/pkg/address-resolver/store"
)

const (
	redisPrefix = "address-resolver"
	redisScheme = "redis://"
)

var (
	addr            string
	metricsAddr     string
	redisURL        string
	redisPoolSize   int
	tag             string
	logLvl          string
	testing         bool
	dmsgDisc        string
	whitelistKeys   string
	testEnvironment bool
	sk              cipher.SecKey
	dmsgPort        uint16
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9093", "address to bind to\033[0m")
	RootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to\033[0m")
	RootCmd.Flags().StringVar(&redisURL, "redis", "redis://localhost:6379", "connections string for a redis store\033[0m")
	RootCmd.Flags().IntVar(&redisPoolSize, "redis-pool-size", 10, "redis connection pool size\033[0m")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "info", "set log level one of: info, error, warn, debug, trace, panic")
	RootCmd.Flags().StringVar(&tag, "tag", "address_resolver", "logging tag\033[0m")
	RootCmd.Flags().BoolVarP(&testing, "testing", "t", false, "enable testing to start without redis\033[0m")
	RootCmd.Flags().StringVar(&dmsgDisc, "dmsg-disc", "http://dmsgd.skywire.skycoin.com", "url of dmsg-discovery\033[0m")
	RootCmd.Flags().StringVar(&whitelistKeys, "whitelist-keys", "", "list of whitelisted keys of network monitor used for deregistration\033[0m")
	RootCmd.Flags().BoolVar(&testEnvironment, "test-environment", false, "distinguished between prod and test environment\033[0m")
	RootCmd.Flags().Var(&sk, "sk", "dmsg secret key\r")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgPort", dmsg.DefaultDmsgHTTPPort, "dmsg port value\r")
	var helpflag bool
	RootCmd.SetUsageTemplate(help)
	RootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for address-resolver")
	RootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	RootCmd.PersistentFlags().MarkHidden("help") //nolint
}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Address Resolver Server for skywire",
	Long: `
	┌─┐┌┬┐┌┬┐┬─┐┌─┐┌─┐┌─┐   ┬─┐┌─┐┌─┐┌─┐┬ ┬  ┬┌─┐┬─┐
	├─┤ ││ ││├┬┘├┤ └─┐└─┐───├┬┘├┤ └─┐│ ││ └┐┌┘├┤ ├┬┘
	┴ ┴─┴┘─┴┘┴└─└─┘└─┘└─┘   ┴└─└─┘└─┘└─┘┴─┘└┘ └─┘┴└─

depends: redis

Note: the specified port must be accessible from the internet ip address or port forwarded for udp
skywire cli config gen-keys > ar-config.json
skywire svc ar --addr ":9093" --redis "redis://localhost:6379" --sk $(tail -n1 ar-config.json)
`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		if !strings.HasPrefix(redisURL, redisScheme) {
			redisURL = redisScheme + redisURL
		}
		storeConfig := storeconfig.Config{
			Type:     storeconfig.Redis,
			URL:      redisURL,
			Password: storeconfig.RedisPassword(),
			PoolSize: redisPoolSize,
		}

		if testing {
			storeConfig.Type = storeconfig.Memory
		}

		logger := logging.MustGetLogger(tag)
		lvl, err := logging.LevelFromString(logLvl)
		if err != nil {
			logger.Fatal("Invalid loglvl detected")
		}

		logging.SetLevel(lvl)

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		transportStore, err := store.New(ctx, storeConfig, logger)
		if err != nil {
			logger.Fatal("Failed to initialize redis store: ", err)
		}

		var whitelistPKs []string
		if whitelistKeys != "" {
			whitelistPKs = strings.Split(whitelistKeys, ",")
		} else {
			if testEnvironment {
				whitelistPKs = strings.Split(skyenv.TestNetworkMonitorPKs, ",")
			} else {
				whitelistPKs = strings.Split(skyenv.NetworkMonitorPKs, ",")
			}
		}

		for _, v := range whitelistPKs {
			api.WhitelistPKs.Set(v)
		}

		nonceStore, err := httpauth.NewNonceStore(ctx, storeConfig, redisPrefix)
		if err != nil {
			logger.Fatal("Failed to initialize redis nonce store: ", err)
		}

		pk, err := sk.PubKey()
		if err != nil {
			logger.WithError(err).Warn("No SecKey found. Skipping serving on dmsghttp.")
		}

		metricsutil.ServeHTTPMetrics(logger, metricsAddr)

		var m armetrics.Metrics
		if metricsAddr == "" {
			m = armetrics.NewEmpty()
		} else {
			m = armetrics.NewVictoriaMetrics()
		}

		var dmsgAddr string
		if !pk.Null() {
			dmsgAddr = fmt.Sprintf("%s:%d", pk.Hex(), dmsgPort)
		}

		enableMetrics := metricsAddr != ""
		arAPI := api.New(logger, transportStore, nonceStore, enableMetrics, m, dmsgAddr)

		udpListener, err := kcp.Listen(addr)
		if err != nil {
			log.Fatal("Failed to open UDP listener: ", err)
		}

		go arAPI.ListenUDP(udpListener)

		if logger != nil {
			logger.Infof("Listening on %s", addr)
		}

		go func() {
			if err := tcpproxy.ListenAndServe(addr, arAPI); err != nil {
				logger.Errorf("tcpproxy.ListenAndServe: %v", err)
				cancel()
			}
		}()

		if !pk.Null() {
			servers := dmsghttp.GetServers(ctx, dmsgDisc, logger)

			var keys cipher.PubKeys
			keys = append(keys, pk)
			dClient := direct.NewClient(direct.GetAllEntries(keys, servers), logger)
			config := &dmsg.Config{
				MinSessions:    0, // listen on all available servers
				UpdateInterval: dmsg.DefaultUpdateInterval,
			}

			dmsgDC, closeDmsgDC, err := direct.StartDmsg(ctx, logger, pk, sk, dClient, config)
			if err != nil {
				logger.WithError(err).Fatal("failed to start direct dmsg client.")
			}

			defer closeDmsgDC()

			go func() {
				for {
					arAPI.DmsgServers = dmsgDC.ConnectedServersPK()
					time.Sleep(time.Second)
				}
			}()

			go dmsghttp.UpdateServers(ctx, dClient, dmsgDisc, dmsgDC, logger)

			go func() {
				if err := dmsghttp.ListenAndServe(ctx, sk, arAPI, dClient, dmsg.DefaultDmsgHTTPPort, dmsgDC, logger); err != nil {
					logger.Errorf("dmsghttp.ListenAndServe: %v", err)
					cancel()
				}
			}()
		}

		<-ctx.Done()

		arAPI.Close()
	},
}

// Execute executes root CLI command.
func Execute() {
	cc.Init(&cc.Config{
		RootCmd:       RootCmd,
		Headings:      cc.HiBlue + cc.Bold, //+ cc.Underline,
		Commands:      cc.HiBlue + cc.Bold,
		CmdShortDescr: cc.HiBlue,
		Example:       cc.HiBlue + cc.Italic,
		ExecName:      cc.HiBlue + cc.Bold,
		Flags:         cc.HiBlue + cc.Bold,
		//FlagsDataType: cc.HiBlue,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)

	}
}

const help = "Usage:\r\n" +
	"  {{.UseLine}}{{if .HasAvailableSubCommands}}{{end}} {{if gt (len .Aliases) 0}}\r\n\r\n" +
	"{{.NameAndAliases}}{{end}}{{if .HasAvailableSubCommands}}\r\n\r\n" +
	"Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand)}}\r\n  " +
	"{{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}\r\n\r\n" +
	"Flags:\r\n" +
	"{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}\r\n\r\n" +
	"Global Flags:\r\n" +
	"{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}\r\n\r\n"
