// geoip.go
// Combined CLI and API server for GeoIP lookup using geoip2-golang/v2 and cobra

package commands

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/oschwald/geoip2-golang/v2"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/spf13/cobra"
)

type LookupResult struct {
	IP         string   `json:"ip"`
	Country    string   `json:"country"`
	CountryISO string   `json:"country_iso"`
	Region     string   `json:"region"`
	City       string   `json:"city"`
	Latitude   *float64 `json:"latitude"`
	Longitude  *float64 `json:"longitude"`
}

var (
	addr    string
	tag     string
	dbPath  string
	apiMode bool
	logLvl  string
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":8080", "address to bind to\033[0m")
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
	Long: calvin.AsciiFont("GeoIP") + `

Note: GeoIP database should downloaded before start. You can get it from https://deb.skywire.dev/GeoLite2-City.mmdb
skywire svc geoip x.x.x.x --db ./GeoLite2-City.mmdb
skywire svc geoip --api --addr ":9093" --db ./GeoLite2-City.mmdb
`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		if dbPath == "" {
			dbPath = "./GeoLite2-City.mmdb"
		}

		db, err := geoip2.Open(dbPath)
		if err != nil {
			log.Fatalf("failed to open GeoIP database: %v", err)
		}
		defer db.Close()

		if apiMode {
			startAPIServer(db, addr)
			return
		}

		if len(args) != 1 {
			log.Fatalf("IP argument is required in CLI mode")
		}

		res, err := lookupIP(db, args[0])
		if err != nil {
			log.Fatalf("lookup failed: %v", err)
		}

		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
	},
}

// Execute executes root CLI command
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

func lookupIP(db *geoip2.Reader, ipStr string) (*LookupResult, error) {
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

	var region string
	if len(record.Subdivisions) > 0 {
		region = record.Subdivisions[0].Names.English
	}

	countryISO := record.Country.ISOCode

	res := &LookupResult{
		IP:         ipStr,
		Country:    record.Country.Names.English,
		CountryISO: countryISO,
		Region:     region,
		City:       record.City.Names.English,
		Latitude:   lat,
		Longitude:  lon,
	}
	return res, nil
}

func startAPIServer(db *geoip2.Reader, addr string) {
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
		_ = enc.Encode(res)
	})

	log.Printf("API server listening %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func ipFromRequest(r *http.Request) string {
	real := strings.TrimSpace(r.Header.Get("X-Real-Ip"))
	if real != "" {
		return real
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
