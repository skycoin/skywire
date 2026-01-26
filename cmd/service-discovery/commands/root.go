// Package commands cmd/service-discovery/commands/root.go
package commands

import (
	"context"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/skycoin/dmsg/pkg/direct"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/internal/sdmetrics"
	"github.com/skycoin/skywire/pkg/service-discovery/api"
	"github.com/skycoin/skywire/pkg/service-discovery/store"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/storeconfig"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/tcpproxy"
)

var log = logging.MustGetLogger("service-discovery")

const redisPrefix = "service-discovery"

var (
	addr           string
	metricsAddr    string
	redisURL       string
	testMode       bool
	dmsgDisc       string
	whitelistKeys  string
	sk             cipher.SecKey
	dmsgPort       uint16
	dmsgServerType string
	geoipURL       string
	pprofAddr      string
	entryTimeout   time.Duration
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9098", "address to bind to\033[0m")
	RootCmd.Flags().StringVarP(&metricsAddr, "metrics", "m", "", "address to bind metrics API to\033[0m")
	RootCmd.Flags().StringVar(&pprofAddr, "pprof", "", "address to bind pprof debug server (e.g. localhost:6060)\033[0m")
	RootCmd.Flags().StringVarP(&redisURL, "redis", "r", "redis://localhost:6379", "connections string for a redis store\033[0m")
	RootCmd.Flags().StringVarP(&whitelistKeys, "whitelist-keys", "w", "", "list of whitelisted keys of network monitor used for deregistration\033[0m")
	RootCmd.Flags().BoolVarP(&testMode, "test", "t", false, "run in test mode and disable auth\033[0m")
	RootCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "d", dmsg.DiscAddr(false), "url of dmsg-discovery\033[0m")
	RootCmd.Flags().StringVar(&geoipURL, "geoip", skyenv.GeoIP, "url of geoip service\033[0m")
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler\033[0m")
	RootCmd.Flags().VarP(&sk, "sk", "s", "dmsg secret key\033[0m\n\r")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgPort", dmsg.DefaultDmsgHTTPPort, "dmsg port value\033[0m")
	RootCmd.Flags().DurationVar(&entryTimeout, "entry-timeout", 15*time.Minute, "timeout for service entry expiration\033[0m")
}

// RootCmd contains the root service-discovery command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Service discovery server",
	Long: calvin.AsciiFont("service-discovery") + `
----- depends: redis -----
keys-gen | tee sd-config.json
service-discovery --sk $(tail -n1 sd-config.json)`,
	Run: func(_ *cobra.Command, _ []string) {
		if dmsgDisc == "" {
			dmsgDisc = dmsg.DiscAddr(false)
		}
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		pk, err := sk.PubKey()
		if err != nil {
			log.WithError(err).Warn("No SecKey found. Skipping serving on dmsghttp.")
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()

		if pprofAddr != "" {
			pprofMux := http.NewServeMux()

			// Register the index (which links to everything else)
			pprofMux.HandleFunc("/debug/pprof/", pprof.Index)
			pprofMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
			pprofMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
			pprofMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
			pprofMux.HandleFunc("/debug/pprof/trace", pprof.Trace)

			// Register profile handlers using pprof.Handler
			for _, profile := range []string{"heap", "goroutine", "threadcreate", "block", "mutex", "allocs"} {
				pprofMux.Handle("/debug/pprof/"+profile, pprof.Handler(profile))
			}

			go func() {
				log.Infof("Starting pprof server on %s", pprofAddr)
				server := &http.Server{
					Addr:              pprofAddr,
					Handler:           pprofMux,
					ReadHeaderTimeout: 10 * time.Second,
					ReadTimeout:       30 * time.Second,
					WriteTimeout:      30 * time.Second,
					IdleTimeout:       60 * time.Second,
				}
				if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Errorf("pprof server failed: %v", err)
				}
			}()

			time.Sleep(100 * time.Millisecond)
		}

		redisPassword := storeconfig.RedisPassword()
		opt, err := redis.ParseURL(redisURL)
		if err != nil {
			log.Fatalf("Failed to parse redis URL: %v", err)
		}
		opt.Password = redisPassword

		redisClient := redis.NewClient(opt)
		if _, err := redisClient.Ping(ctx).Result(); err != nil {
			log.Fatalf("Failed to connect to redis: %v", err)
		}
		log.Printf("Redis connected.")

		db, err := store.NewStore(ctx, redisClient, log, entryTimeout)
		if err != nil {
			log.Fatal("Failed to initialize redis store: ", err)
		}
		log.Printf("Service entry timeout: %v", entryTimeout)

		var nonceDB httpauth.NonceStore
		if !testMode {
			nonceStoreConfig := storeconfig.Config{
				URL:      redisURL,
				Type:     storeconfig.Redis,
				Password: storeconfig.RedisPassword(),
			}
			nonceDB, err = httpauth.NewNonceStore(ctx, nonceStoreConfig, redisPrefix)
			if err != nil {
				log.Fatal("Failed to initialize redis nonce store: ", err)
			}
		}

		metricsutil.ServeHTTPMetrics(log, metricsAddr)

		var m sdmetrics.Metrics
		if metricsAddr == "" {
			m = sdmetrics.NewEmpty()
		} else {
			m = sdmetrics.NewVictoriaMetrics()
		}

		var dmsgAddr string
		if !pk.Null() {
			dmsgAddr = fmt.Sprintf("%s:%d", pk.Hex(), dmsgPort)
		}

		// we enable metrics middleware if address is passed
		enableMetrics := metricsAddr != ""
		sdAPI := api.New(log, db, nonceDB, enableMetrics, m, dmsgAddr, geoipURL)

		var whitelistPKs []string
		if whitelistKeys != "" {
			whitelistPKs = strings.Split(whitelistKeys, ",")
		}
		for _, v := range whitelistPKs {
			api.WhitelistPKs.Set(v)
		}

		go sdAPI.RunBackgroundTasks(ctx, log)

		log.WithField("addr", addr).Info("Serving discovery API...")
		go func() {
			if err := tcpproxy.ListenAndServe(addr, sdAPI); err != nil {
				log.Errorf("ListenAndServe: %v", err)
				cancel()
			}
		}()

		if !pk.Null() {
			servers := dmsghttp.GetServers(ctx, dmsgDisc, dmsgServerType, log)
			config := &dmsg.Config{
				MinSessions:          0, // listen on all available servers
				UpdateInterval:       dmsg.DefaultUpdateInterval,
				ConnectedServersType: dmsgServerType,
			}
			var keys cipher.PubKeys
			keys = append(keys, pk)
			dClient := direct.NewClient(direct.GetAllEntries(keys, servers), log)

			dmsgDC, closeDmsgDC, err := direct.StartDmsg(ctx, log, pk, sk, dClient, config)
			if err != nil {
				log.WithError(err).Fatal("failed to start direct dmsg client.")
			}

			defer closeDmsgDC()

			go func() {
				for {
					sdAPI.DmsgServers = dmsgDC.ConnectedServersPK()
					time.Sleep(time.Second)
				}
			}()

			go dmsghttp.UpdateServers(ctx, dClient, dmsgDisc, dmsgDC, dmsgServerType, log)

			go func() {
				if err := dmsghttp.ListenAndServe(ctx, sk, sdAPI, dClient, dmsg.DefaultDmsgHTTPPort, dmsgDC, log); err != nil {
					log.Errorf("dmsghttp.ListenAndServe: %v", err)
					cancel()
				}
			}()
		}

		<-ctx.Done()
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
