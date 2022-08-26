// Package commands root.go
package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/setup"
	"github.com/skycoin/skywire/pkg/setup/setupmetrics"
	"github.com/skycoin/skywire/pkg/syslog"
)

var (
	metricsAddr  string
	syslogAddr   string
	tag          string
	cfgFromStdin bool
)

func init() {
	rootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to")
	rootCmd.Flags().StringVar(&syslogAddr, "syslog", "", "syslog server address. E.g. localhost:514")
	rootCmd.Flags().StringVar(&tag, "tag", "setup_node", "logging tag")
	rootCmd.Flags().BoolVarP(&cfgFromStdin, "stdin", "i", false, "read config from STDIN")
}

var rootCmd = &cobra.Command{
	Use:   "setup-node [config.json]",
	Short: "Route Setup Node for skywire",
	Long: `
	┌─┐┌─┐┌┬┐┬ ┬┌─┐   ┌┐┌┌─┐┌┬┐┌─┐
	└─┐├┤  │ │ │├─┘───││││ │ ││├┤
	└─┘└─┘ ┴ └─┘┴     ┘└┘└─┘─┴┘└─┘`,

	Run: func(_ *cobra.Command, args []string) {
		mLog := logging.NewMasterLogger()
		log := logging.MustGetLogger(tag)

		if _, err := buildinfo.Get().WriteTo(mLog.Out); err != nil {
			mLog.Printf("Failed to output build info: %v", err)
		}

		if syslogAddr != "" {
			hook, err := syslog.SetupHook(syslogAddr, tag)
			if err != nil {
				log.Fatalf("Error setting up syslog: %v", err)
			}

			logging.AddHook(hook)
		}

		var rdr io.Reader
		var err error

		if !cfgFromStdin {
			configFile := "config.json"

			if len(args) > 0 {
				configFile = args[0]
			}
			rdr, err = os.Open(configFile)
			if err != nil {
				log.Fatalf("Failed to open config: %v", err)
			}
		} else {
			log.Info("Reading config from STDIN")
			rdr = bufio.NewReader(os.Stdin)
		}

		conf := &setup.Config{}

		raw, err := io.ReadAll(rdr)
		if err != nil {
			log.Fatalf("Failed to read config: %v", err)
		}

		if err := json.Unmarshal(raw, &conf); err != nil {
			log.WithField("raw", string(raw)).Fatalf("Failed to decode config: %s", err)
		}

		log.Infof("Config: %#v", conf)

		sn, err := setup.NewNode(conf)
		if err != nil {
			log.Fatal("Failed to create a setup node: ", err)
		}

		m := prepareMetrics(log)

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		log.Fatal(sn.Serve(ctx, m))
	},
}

func prepareMetrics(log logrus.FieldLogger) setupmetrics.Metrics {
	if metricsAddr == "" {
		return setupmetrics.NewEmpty()
	}

	m := setupmetrics.NewVictoriaMetrics()

	metricsutil.ServeHTTPMetrics(log, metricsAddr)

	// TODO (darkrengarius): implement these with Victoria Metrics somehow
	//reg.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	//reg.MustRegister(prometheus.NewGoCollector())

	return m
}

// Execute executes root CLI command.
func Execute() {
	cc.Init(&cc.Config{
		RootCmd:         rootCmd,
		Headings:        cc.HiBlue + cc.Bold,
		Commands:        cc.HiBlue + cc.Bold,
		CmdShortDescr:   cc.HiBlue,
		Example:         cc.HiBlue + cc.Italic,
		ExecName:        cc.HiBlue + cc.Bold,
		Flags:           cc.HiBlue + cc.Bold,
		FlagsDataType:   cc.HiBlue,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})
	if err := rootCmd.Execute(); err != nil {
		panic(err)
	}
}
