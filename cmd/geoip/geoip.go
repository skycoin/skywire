// geoip-cli-api.go
// Combined CLI and API server for GeoIP lookup using geoip2-golang and cobra

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"

	"github.com/oschwald/geoip2-golang/v2"
	"github.com/spf13/cobra"
)

type LookupResult struct {
	IP             string   `json:"ip"`
	Country        string   `json:"country"`
	CountryISO     string   `json:"country_iso,omitempty"`
	Region         string   `json:"region,omitempty"`
	City           string   `json:"city,omitempty"`
	Latitude       *float64 `json:"latitude,omitempty"`
	Longitude      *float64 `json:"longitude,omitempty"`
	AccuracyRadius *uint16  `json:"accuracy_radius,omitempty"`
}

func main() {
	var dbPath string
	var apiMode bool

	rootCmd := &cobra.Command{
		Use:   "geoip [ip]",
		Short: "GeoIP lookup CLI and API server",
		Args:  cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if dbPath == "" {
				dbPath = "./GeoLite2-City.mmdb"
			}

			db, err := geoip2.Open(dbPath)
			if err != nil {
				log.Fatalf("failed to open GeoIP database: %v", err)
			}
			defer db.Close()

			if apiMode {
				startAPIServer(db)
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

	rootCmd.Flags().StringVar(&dbPath, "db", "", "Path to GeoLite2-City.mmdb database")
	rootCmd.Flags().BoolVar(&apiMode, "api", false, "Run as API server")

	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
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

func startAPIServer(db *geoip2.Reader) {
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

	addr := ":8080"
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
