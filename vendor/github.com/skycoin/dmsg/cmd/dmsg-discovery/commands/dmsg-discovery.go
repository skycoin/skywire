// Package commands cmd/dmsg-discovery/commands/root.go
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"strings"
	"time"

	proxyproto "github.com/pires/go-proxyproto"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/metricsutil"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	"github.com/skycoin/dmsg/internal/discmetrics"
	"github.com/skycoin/dmsg/internal/dmsg-discovery/api"
	"github.com/skycoin/dmsg/internal/dmsg-discovery/store"
	"github.com/skycoin/dmsg/pkg/direct"
	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
)

const redisPasswordEnvName = "REDIS_PASSWORD"

var (
	sf                cmdutil.ServiceFlags
	addr              string
	redisURL          string
	whitelistKeys     string
	entryTimeout      time.Duration
	testMode          bool
	enableLoadTesting bool
	testEnvironment   bool
	pk                cipher.PubKey
	sk                cipher.SecKey
	dmsgPort          uint16
	authPassphrase    string
	officialServers   string
	dmsgServerType    string
	pprofAddr         string
)

// exampleJSON marshals v to indented JSON with color, returning empty string on error
func exampleJSON(v interface{}) string {
	b, err := json.MarshalIndent(v, "    ", "  ")
	if err != nil {
		return ""
	}
	return string(pretty.Color(b, nil))
}

// generateExamples creates example responses from actual struct types
func generateExamples() string {
	// Use actual build info with fallbacks
	bi := buildinfo.Get()
	version := bi.Version
	if version == "" || version == "unknown" {
		version = "v1.3.29"
	}
	commit := bi.Commit
	if commit == "" || commit == "unknown" {
		commit = "abc1234"
	}
	date := bi.Date
	if date == "" || date == "unknown" {
		date = "2024-01-15T10:30:00Z"
	}

	// Use actual DMSG servers from embedded deployment config
	var serverEntries []disc.Entry
	var serverPKs []string
	if len(dmsg.Prod.DmsgServers) > 0 {
		// Use up to 2 real servers for examples
		limit := 2
		if len(dmsg.Prod.DmsgServers) < limit {
			limit = len(dmsg.Prod.DmsgServers)
		}
		for i := 0; i < limit; i++ {
			serverEntries = append(serverEntries, dmsg.Prod.DmsgServers[i])
			serverPKs = append(serverPKs, dmsg.Prod.DmsgServers[i].Static.Hex())
		}
	}

	// Fallback example PKs if no servers available
	exClientPK := "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"
	exClientPK2 := "024ec47420176680816e0406250e7156465e4531f5b26057c9f6297bb0303558c7"

	// GET /health - use first real server PK if available
	dmsgAddrPK := exClientPK
	if len(serverPKs) > 0 {
		dmsgAddrPK = serverPKs[0]
	}
	healthExample := map[string]interface{}{
		"build_info": map[string]interface{}{
			"version": version,
			"commit":  commit,
			"date":    date,
		},
		"started_at":   "2024-01-15T10:00:00Z",
		"dmsg_address": dmsgAddrPK + ":80",
		"dmsg_servers": serverPKs,
	}

	// disc.Entry (client) - use real server PKs for delegated_servers
	delegatedServers := serverPKs
	if len(delegatedServers) == 0 {
		delegatedServers = []string{"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"}
	}
	clientEntryExample := map[string]interface{}{
		"version":   "1.0",
		"sequence":  1,
		"timestamp": 1705315200,
		"static":    exClientPK,
		"client": map[string]interface{}{
			"delegated_servers": delegatedServers,
		},
	}

	// POST response - disc.HTTPMessage
	entrySetExample := map[string]interface{}{
		"code":    200,
		"message": "wrote a new entry",
	}
	entryUpdatedExample := map[string]interface{}{
		"code":    200,
		"message": "wrote new entry iteration",
	}
	entryDeletedExample := map[string]interface{}{
		"code":    200,
		"message": "deleted entry",
	}

	// GET /dmsg-discovery/servers/clients - map[server_pk][]client_pk
	clientsByServerExample := make(map[string][]string)
	if len(serverPKs) > 0 {
		clientsByServerExample[serverPKs[0]] = []string{exClientPK, exClientPK2}
	} else {
		clientsByServerExample["03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"] = []string{exClientPK, exClientPK2}
	}

	// GET /dmsg-discovery/server/{pk}/clients - []client_pk
	clientsForServerExample := []string{exClientPK, exClientPK2}

	// Use real server entries if available, otherwise use fallback
	var serverEntryForExample interface{}
	var serverEntriesForList []interface{}
	if len(serverEntries) > 0 {
		serverEntryForExample = serverEntries[0]
		for _, entry := range serverEntries {
			serverEntriesForList = append(serverEntriesForList, entry)
		}
	} else {
		// Fallback server entry
		serverEntryForExample = map[string]interface{}{
			"version":   "1.0",
			"sequence":  1,
			"timestamp": 1705315200,
			"static":    "03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e",
			"server": map[string]interface{}{
				"address":           "192.168.1.100:8081",
				"available_streams": 100,
				"max_streams":       200,
				"server_type":       "official",
			},
		}
		serverEntriesForList = []interface{}{serverEntryForExample}
	}

	// Arrays for list endpoints
	entriesExample := append([]interface{}{clientEntryExample}, serverEntriesForList...)
	visorEntriesExample := []interface{}{clientEntryExample}

	return fmt.Sprintf(`
Response Examples:

GET /health
%s

GET /dmsg-discovery/entry/{pk} (client entry)
%s

GET /dmsg-discovery/entry/{pk} (server entry)
%s

POST /dmsg-discovery/entry/ (new entry)
%s

POST /dmsg-discovery/entry/ (update entry)
%s

DEL /dmsg-discovery/entry
%s

GET /dmsg-discovery/entries (all client and server entries)
%s

GET /dmsg-discovery/visorEntries (client entries only)
%s

GET /dmsg-discovery/available_servers (servers with available_streams > 0)
%s

GET /dmsg-discovery/all_servers (all server entries)
%s

GET /dmsg-discovery/servers/clients
%s

GET /dmsg-discovery/server/{pk}/clients
%s`,
		exampleJSON(healthExample),
		exampleJSON(clientEntryExample),
		exampleJSON(serverEntryForExample),
		exampleJSON(entrySetExample),
		exampleJSON(entryUpdatedExample),
		exampleJSON(entryDeletedExample),
		exampleJSON(entriesExample),
		exampleJSON(visorEntriesExample),
		exampleJSON(serverEntriesForList),
		exampleJSON(serverEntriesForList),
		exampleJSON(clientsByServerExample),
		exampleJSON(clientsForServerExample))
}

func init() {
	sf.Init(RootCmd, "dmsg_disc", "")

	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9090", "address to bind to\n\r")
	RootCmd.Flags().StringVar(&pprofAddr, "pprof", "", "address to bind pprof debug server (e.g. localhost:6060)")
	RootCmd.Flags().StringVar(&authPassphrase, "auth", "", "auth passphrase as simple auth for official dmsg servers registration")
	RootCmd.Flags().StringVar(&officialServers, "official-servers", "", "list of official dmsg servers keys separated by comma")
	RootCmd.Flags().StringVar(&redisURL, "redis", store.DefaultURL, "connections string for a redis store\n\r")
	RootCmd.Flags().StringVar(&whitelistKeys, "whitelist-keys", "", "list of whitelisted keys of network monitor used for deregistration")
	RootCmd.Flags().DurationVar(&entryTimeout, "entry-timeout", store.DefaultTimeout, "discovery entry timeout\n\r")
	RootCmd.Flags().BoolVarP(&testMode, "test-mode", "t", false, "in testing mode")
	RootCmd.Flags().BoolVar(&enableLoadTesting, "enable-load-testing", false, "enable load testing")
	RootCmd.Flags().BoolVar(&testEnvironment, "test-environment", false, "distinguished between prod and test environment")
	RootCmd.Flags().Var(&sk, "sk", "dmsg secret key\n\r")
	RootCmd.Flags().Uint16Var(&dmsgPort, "dmsgPort", dmsg.DefaultDmsgHTTPPort, "dmsg port value\n\r")
	RootCmd.Flags().StringVar(&dmsgServerType, "dmsg-server-type", "", "type of dmsg server on dmsghttp handler")
}

// RootCmd contains commands for dmsg-discovery
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "DMSG Discovery Server",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐  ┌┬┐┬┌─┐┌─┐┌─┐┬  ┬┌─┐┬─┐┬ ┬
	 │││││└─┐│ ┬───│││└─┐│  │ │└┐┌┘├┤ ├┬┘└┬┘
	─┴┘┴ ┴└─┘└─┘  ─┴┘┴└─┘└─┘└─┘ └┘ └─┘┴└─ ┴
DMSG Discovery Server - registers and discovers DMSG clients and servers.

Depends: redis

HTTP Endpoints:
  GET  /health                                Health check
  GET  /dmsg-discovery/entry/{pk}             Get entry by public key
  POST /dmsg-discovery/entry/                 Register/update entry
  POST /dmsg-discovery/entry/{pk}             Register/update entry
  DEL  /dmsg-discovery/entry                  Delete entry
  GET  /dmsg-discovery/entries                All entries
  GET  /dmsg-discovery/visorEntries           All visor entries
  DEL  /dmsg-discovery/deregister             Deregister entry
  GET  /dmsg-discovery/available_servers      Available DMSG servers
  GET  /dmsg-discovery/all_servers            All DMSG servers
  GET  /dmsg-discovery/servers/clients        Clients by all servers
  GET  /dmsg-discovery/server/{pk}/clients    Clients by specific server
` + generateExamples() + `

Example:
  skywire cli config gen-keys > dmsgd-config.json
  skywire dmsg disc --sk $(tail -n1 dmsgd-config.json)`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		log := sf.Logger()

		var err error
		if pk, err = sk.PubKey(); err != nil {
			log.WithError(err).Warn("No SecKey found. Skipping serving on dmsghttp.")
		}

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

		metricsutil.ServeHTTPMetrics(log, sf.MetricsAddr)

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()
		db := prepareDB(ctx, log)

		var m discmetrics.Metrics
		if sf.MetricsAddr == "" {
			m = discmetrics.NewEmpty()
		} else {
			m = discmetrics.NewVictoriaMetrics()
		}

		var dmsgAddr string
		if !pk.Null() {
			dmsgAddr = fmt.Sprintf("%s:%d", pk.Hex(), dmsgPort)
		}

		// we enable metrics middleware if address is passed
		enableMetrics := sf.MetricsAddr != ""
		a := api.New(log, db, m, testMode, enableLoadTesting, enableMetrics, dmsgAddr, authPassphrase)

		var whitelistPKs []string
		if whitelistKeys != "" {
			whitelistPKs = strings.Split(whitelistKeys, ",")
		}

		for _, v := range whitelistPKs {
			api.WhitelistPKs.Set(v)
		}

		a.OfficialServers, err = fetchOfficialDmsgServers(officialServers)
		if err != nil {
			log.Info(err)
		}

		go a.RunBackgroundTasks(ctx, log)
		log.WithField("addr", addr).Info("Serving discovery API...")
		go func() {
			if err = listenAndServe(addr, a); err != nil {
				log.Errorf("ListenAndServe: %v", err)
				cancel()
			}
		}()
		if !pk.Null() {
			servers := getServers(ctx, a, dmsgServerType, log)
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
					a.DmsgServers = dmsgDC.ConnectedServersPK()
					time.Sleep(time.Second)
				}
			}()

			go updateServers(ctx, a, dClient, dmsgDC, dmsgServerType, log)

			go func() {
				if err = dmsghttp.ListenAndServe(ctx, sk, a, dClient, dmsg.DefaultDmsgHTTPPort, dmsgDC, log); err != nil {
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

func prepareDB(ctx context.Context, log *logging.Logger) store.Storer {
	dbConf := &store.Config{
		URL:      redisURL,
		Password: os.Getenv(redisPasswordEnvName),
		Timeout:  entryTimeout,
	}

	db, err := store.NewStore(ctx, "redis", dbConf, log)
	if err != nil {
		log.Fatal("Failed to initialize redis store: ", err)
	}

	return db
}

func getServers(ctx context.Context, a *api.API, dmsgServerType string, log logrus.FieldLogger) (servers []*disc.Entry) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		servers, err := a.AllServers(ctx, log)
		if err != nil {
			log.WithError(err).Fatal("Error getting dmsg-servers.")
		}
		// filtered dmsg servers by their type
		if dmsgServerType != "" {
			var filteredServers []*disc.Entry
			for _, server := range servers {
				if server.Server.ServerType == dmsgServerType {
					filteredServers = append(filteredServers, server)
				}
			}
			servers = filteredServers
		}
		if len(servers) > 0 {
			return servers
		}
		log.Warn("No dmsg-servers found, trying again in 1 minute.")
		select {
		case <-ctx.Done():
			return []*disc.Entry{}
		case <-ticker.C:
			getServers(ctx, a, dmsgServerType, log)
		}
	}
}

func updateServers(ctx context.Context, a *api.API, dClient disc.APIClient, dmsgC *dmsg.Client, dmsgServerType string, log logrus.FieldLogger) {
	ticker := time.NewTicker(time.Minute * 10)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			servers, err := a.AllServers(ctx, log)
			if err != nil {
				log.WithError(err).Error("Error getting dmsg-servers.")
				break
			}
			// filtered dmsg servers by their type
			if dmsgServerType != "" {
				var filteredServers []*disc.Entry
				for _, server := range servers {
					if server.Server.ServerType == dmsgServerType {
						filteredServers = append(filteredServers, server)
					}
				}
				servers = filteredServers
			}
			for _, server := range servers {
				dClient.PostEntry(ctx, server) //nolint
				err := dmsgC.EnsureSession(ctx, server)
				if err != nil {
					log.WithField("remote_pk", server.Static).WithError(err).Warn("Failed to establish session.")
				}
			}
		}
	}
}

func listenAndServe(addr string, handler http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: handler, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 30 * time.Second, ReadHeaderTimeout: 3 * time.Second}
	if addr == "" {
		addr = ":http"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	proxyListener := &proxyproto.Listener{Listener: ln}
	defer proxyListener.Close() // nolint:errcheck
	return srv.Serve(proxyListener)
}

func fetchOfficialDmsgServers(officialServers string) (map[string]bool, error) {
	dmsgServers := make(map[string]bool)
	if officialServers != "" {
		dmsgServersList := strings.Split(officialServers, ",")
		for _, v := range dmsgServersList {
			dmsgServers[v] = true
		}
		return dmsgServers, nil
	}
	return dmsgServers, errors.New("no official dmsg server list passed by --official-server flag")
}
