// Package commands cmd/route-finder/commands/geoip.go
package commands

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/oschwald/geoip2-golang/v2"
	"github.com/spf13/cobra"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
)

//go:embed GeoLite2-City.mmdb
var embeddedGeoIP []byte

type lookupResult struct {
	IP            string   `json:"ip_address"`
	Latitude      *float64 `json:"latitude"`
	Longitude     *float64 `json:"longitude"`
	PostalCode    string   `json:"postal_code"`
	ContinentCode string   `json:"continent_code"`
	ContinentName string   `json:"continent_name"`
	CountryCode   string   `json:"country_code"`
	CountryName   string   `json:"country_name"`
	RegionCode    string   `json:"region_code"`
	RegionName    string   `json:"region_name"`
	CityName      string   `json:"city_name"`
	Timezone      string   `json:"timezone"`
}

var (
	addr      string
	tag       string
	dbPath    string
	apiMode   bool
	logLvl    string
	pprofAddr string
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":8080", "address to bind to\033[0m")
	RootCmd.Flags().StringVar(&pprofAddr, "pprof", "", "address to bind pprof debug server (e.g. localhost:6060)\033[0m")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "info", "[info|error|warn|debug|trace|panic]\033[0m")
	RootCmd.Flags().StringVar(&tag, "tag", "geoip", "logging tag\033[0m")
	RootCmd.Flags().StringVar(&dbPath, "db", "", "Path to GeoLite2-City.mmdb database")
	RootCmd.Flags().BoolVar(&apiMode, "api", false, "Run as API server")
}

// RootCmd is the root command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "GeoIP service for skywire",
	Long: calvin.AsciiFont("geoip") + `

Note: Embeddeed GeoIP database is used by default
Path to local GeoIP database can also be specified via the '--db' flag
skywire svc geoip --db ./GeoLite2-City.mmdb 8.8.8.8
skywire svc geoip --api --addr ":9093" --db ./GeoLite2-City.mmdb
`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		logger := logging.MustGetLogger(tag)
		lvl, err := logging.LevelFromString(logLvl)
		if err != nil {
			logger.Fatal("Invalid loglvl detected")
		}

		logging.SetLevel(lvl)

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
				logger.Infof("Starting pprof server on %s", pprofAddr)
				server := &http.Server{
					Addr:              pprofAddr,
					Handler:           pprofMux,
					ReadHeaderTimeout: 10 * time.Second,
					ReadTimeout:       30 * time.Second,
					WriteTimeout:      30 * time.Second,
					IdleTimeout:       60 * time.Second,
				}
				if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logger.Errorf("pprof server failed: %v", err)
				}
			}()

			time.Sleep(100 * time.Millisecond)
		}

		var db *geoip2.Reader

		if dbPath != "" {
			// External DB path provided
			logger.Infof("Using external GeoIP database: %s", dbPath)
			db, err = geoip2.Open(dbPath)
			if err != nil {
				logger.Fatalf("failed to open GeoIP database from path '%s': %v", dbPath, err)
			}
		} else {
			// Use embedded database
			logger.Info("Using embedded GeoIP database")
			db, err = geoip2.OpenBytes(embeddedGeoIP)
			if err != nil {
				logger.Fatalf("failed to load embedded GeoIP database: %v", err)
			}
		}
		defer func() {
			if err := db.Close(); err != nil {
				logger.Warnf("failed to close GeoIP database: %v", err)
			}
		}()

		if apiMode {
			startAPIServer(db, addr, logger)
			return
		}

		if len(args) != 1 {
			logger.Fatalf("IP argument is required in CLI mode")
		}

		res, err := lookupIP(db, args[0])
		if err != nil {
			logger.Fatalf("lookup failed: %v", err)
		}

		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err = enc.Encode(res); err != nil {
			logger.Warn("failed to encode response: %v", err)
		}
	},
}

// Execute executes root CLI command
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

func lookupIP(db *geoip2.Reader, ipStr string) (*lookupResult, error) {
	parsed, err := netip.ParseAddr(ipStr)
	if err != nil {
		return nil, fmt.Errorf("invalid IP: %s", ipStr)
	}

	record, err := db.City(parsed)
	if err != nil {
		return nil, err
	}

	var lat, lon *float64
	if record.Location.Latitude != nil && record.Location.Longitude != nil {
		lat = record.Location.Latitude
		lon = record.Location.Longitude
	}

	var region geoip2.CitySubdivision
	if len(record.Subdivisions) > 0 {
		region = record.Subdivisions[0]
	}

	res := &lookupResult{
		IP:            ipStr,
		CountryName:   record.Country.Names.English,
		CountryCode:   record.Country.ISOCode,
		RegionName:    region.Names.English,
		RegionCode:    region.ISOCode,
		CityName:      record.City.Names.English,
		Latitude:      lat,
		Longitude:     lon,
		ContinentName: record.Continent.Names.English,
		ContinentCode: record.Continent.Code,
		Timezone:      record.Location.TimeZone,
		PostalCode:    record.Postal.Code,
	}
	return res, nil
}

func startAPIServer(db *geoip2.Reader, addr string, logger *logging.Logger) {
	srv := &http.Server{
		Addr:              addr,
		ReadHeaderTimeout: 10 * time.Second,
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		queryIP := strings.TrimSpace(r.URL.Query().Get("ip"))
		if queryIP == "" {
			queryIP = ipFromRequest(r)
		}

		if queryIP == "" {
			http.Error(w, "could not determine client IP", http.StatusBadRequest)
			return
		}

		res, err := lookupIP(db, queryIP)
		if err != nil {
			http.Error(w, fmt.Sprintf("lookup failed: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err = enc.Encode(res); err != nil {
			http.Error(w, fmt.Sprintf("failed to encode response: %v", err), http.StatusInternalServerError)
			return
		}
	})

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		logger.Printf("API server listening %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("Server failed: %v", err)
		}
	}()

	<-stop
	logger.Println("Shutting down server gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatalf("Server forced to shutdown: %v", err)
	}

	logger.Println("Server stopped")
}

func ipFromRequest(r *http.Request) string {
	realIP := strings.TrimSpace(r.Header.Get("X-Real-Ip"))
	if realIP != "" {
		return realIP
	}
	xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	remote := r.RemoteAddr
	if remote != "" {
		host, _, err := net.SplitHostPort(remote)
		if err == nil {
			return host
		}
		return remote
	}
	return ""
}
