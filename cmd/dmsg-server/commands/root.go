package commands

import (
	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/disc"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	logrussyslog "github.com/sirupsen/logrus/hooks/syslog"
	"github.com/spf13/cobra"
	"log"
	"log/syslog"
	"net"
	"net/http"
	"os"
)

var (
	pubKey       string
	secKey       string
	localAddr    string
	publicAddr   string
	discoveryAddr string
	logLvl       string
	metricsAddr  string
	syslogAddr   string
	tag          string
	cfgFromStdin bool
)

// Config is a dmsg-server config
type Config struct {
	PubKey        cipher.PubKey `json:"public_key"`
	SecKey        cipher.SecKey `json:"secret_key"`
	Discovery     string        `json:"discovery"`
	LocalAddress  string        `json:"local_address"`
	PublicAddress string        `json:"public_address"`
	LogLevel      string        `json:"log_level"`
}

var rootCmd = &cobra.Command{
	Use:   "dmsg-server [config.json]",
	Short: "Dmsg Server for skywire",
	PreRun: func(_ *cobra.Command, _ []string) {
		parseEnvVars()
	},
	Run: func(_ *cobra.Command, args []string) {
		pk := cipher.PubKey{}
		err := pk.Set(pubKey)
		if err != nil {
			log.Fatal(err)
		}
		sk := cipher.SecKey{}
		err = sk.Set(secKey)
		if err != nil {
			log.Fatal(err)
		}

		// Logger
		logger := logging.MustGetLogger(tag)
		logLevel, err := logging.LevelFromString(logLvl)
		if err != nil {
			log.Fatal("Failed to parse LogLevel: ", err)
		}
		logging.SetLevel(logLevel)

		if syslogAddr != "" {
			hook, err := logrussyslog.NewSyslogHook("udp", syslogAddr, syslog.LOG_INFO, tag)
			if err != nil {
				logger.Fatalf("Unable to connect to syslog daemon on %v", syslogAddr)
			}
			logging.AddHook(hook)
		}

		// Metrics
		go func() {
			http.Handle("/metrics", promhttp.Handler())
			if err := http.ListenAndServe(metricsAddr, nil); err != nil {
				logger.Println("Failed to start metrics API:", err)
			}
		}()

		l, err := net.Listen("tcp", localAddr)
		if err != nil {
			logger.Fatalf("Error listening on %s: %v", localAddr, err)
		}

		// Start
		srv, err := dmsg.NewServer(pk, sk, publicAddr, l, disc.NewHTTP(discoveryAddr))
		if err != nil {
			logger.Fatalf("Error creating DMSG server instance: %v", err)
		}

		log.Fatal(srv.Serve())
	},
}

func init() {
	rootCmd.Flags().StringVarP(&pubKey, "public-key", "", "", "service's public key")
	rootCmd.Flags().StringVarP(&secKey, "secret-key", "", "", "service's secret key")
	rootCmd.Flags().StringVarP(&localAddr, "local-address", "", "localhost:8081", "service's local address")
	rootCmd.Flags().StringVarP(&publicAddr, "public-address", "", "", "service's public address")
	rootCmd.Flags().StringVarP(&discoveryAddr, "discovery-address", "d", "localhost:8080", "discovery service address")
	rootCmd.Flags().StringVarP(&logLvl, "log-level", "", "info", "service's public address")
	rootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", ":2121", "address to bind metrics API to")
	rootCmd.Flags().StringVar(&syslogAddr, "syslog", "", "syslog server address. E.g. localhost:514")
	rootCmd.Flags().StringVar(&tag, "tag", "dmsg-server", "logging tag")
	rootCmd.Flags().BoolVarP(&cfgFromStdin, "stdin", "i", false, "read configuration from STDIN")
}

func parseEnvVars() {
	pubKey = os.Getenv("DMSG_SERVER_PK")
	secKey = os.Getenv("DMSG_SERVER_SK")
	localAddr = os.Getenv("DMSG_SERVER_LOCAL_ADDR")
	publicAddr = os.Getenv("DMSG_SERVER_PUBLIC_ADDR")
	discoveryAddr = os.Getenv("DMSG_DISCOVERY_ADDR")
	logLvl = os.Getenv("DMSG_SERVER_LOG_LVL")
}

/*
func parseConfig(configFile string) *Config {
	var rdr io.Reader
	var err error
	if !cfgFromStdin {
		rdr, err = os.Open(filepath.Clean(configFile))
		if err != nil {
			log.Fatalf("Failed to open config: %s", err)
		}
	} else {
		rdr = bufio.NewReader(os.Stdin)
	}

	conf := &Config{}
	if err := json.NewDecoder(rdr).Decode(&conf); err != nil {
		log.Fatalf("Failed to decode %s: %s", rdr, err)
	}

	return conf
}
*/

// Execute executes root CLI command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
