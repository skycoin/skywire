// Package commands root.go
package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/syslog"
)

var (
	metricsAddr  string
	syslogAddr   string
	tag          string
	cfgFromStdin bool
)

func init() {
	RootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to")
	RootCmd.Flags().StringVar(&syslogAddr, "syslog", "", "syslog server address. E.g. localhost:514")
	RootCmd.Flags().StringVar(&tag, "tag", "setup_node", "logging tag")
	RootCmd.Flags().BoolVarP(&cfgFromStdin, "stdin", "i", false, "read config from STDIN")
}

// RootCmd is the root command for setup node
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", ""))+" [config.json]", " ")[0]
	}(),
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

		conf := &router.SetupConfig{}

		raw, err := io.ReadAll(rdr)
		if err != nil {
			log.Fatalf("Failed to read config: %v", err)
		}

		if err := json.Unmarshal(raw, &conf); err != nil {
			log.WithField("raw", string(raw)).Fatalf("Failed to decode config: %s", err)
		}

		log.Infof("Config: %#v", conf)

		sn, err := router.NewNode(conf)
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
	RootCmd.Execute()
}
