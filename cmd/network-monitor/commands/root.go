// Package commands cmd/network-monitor/commands/root.go
package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/network-monitor/api"
	"github.com/skycoin/skywire/pkg/network-monitor/store"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/storeconfig"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/tcpproxy"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	sdURL         string
	arURL         string
	utURL         string
	tpdURL        string
	dmsgdURL      string
	cleaningDelay int
	pk            string
	sk            string
	addr          string
	tag           string
	logLvl        string
	pprofAddr     string
)

// Variables for deregister subcommand
var (
	deregPK       string // Public key(s) to deregister (comma-separated)
	deregType     string // Service type: vpn, visor, skysocks/proxy
	deregSDURL    string // Service discovery URL (only used with --sk)
	deregNMSK     string // Network monitor secret key (optional, uses visor RPC if not set)
	deregAllTypes bool   // Deregister from all service types
	deregRPCAddr  string // Visor RPC address
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", ":9080", "address to bind to.")
	RootCmd.Flags().StringVar(&pprofAddr, "pprof", "", "address to bind pprof debug server (e.g. localhost:6060)")
	RootCmd.Flags().StringVar(&sdURL, "sd-url", "http://sd.skycoin.com", "url to service discovery")
	RootCmd.Flags().StringVar(&arURL, "ar-url", "http://ar.skywire.skycoin.com", "url to address resolver")
	RootCmd.Flags().StringVar(&utURL, "ut-url", "http://ut.skywire.skycoin.com", "url to uptime tracker visor data.")
	RootCmd.Flags().StringVar(&tpdURL, "tpd-url", "http://tpd.skywire.skycoin.com", "url to transport discovery")
	RootCmd.Flags().StringVar(&dmsgdURL, "dmsgd-url", "http://dmsgd.skywire.skycoin.com", "url to dmsg discovery")
	RootCmd.Flags().IntVarP(&cleaningDelay, "cleaning-delay", "d", 75, "time for delay between each service cleaning routine")
	RootCmd.Flags().StringVar(&pk, "pk", "", "pk of network monitor")
	RootCmd.Flags().StringVar(&sk, "sk", "", "sk of network monitor")
	RootCmd.Flags().StringVar(&tag, "tag", "network_monitor", "logging tag")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "info", "[info|error|warn|debug|trace|panic]")

	// Add subcommands
	RootCmd.AddCommand(deregisterCmd)

	// Deregister command flags
	deregisterCmd.Flags().StringVarP(&deregPK, "pk", "p", "", "public key(s) to deregister (comma-separated for multiple)")
	deregisterCmd.Flags().StringVarP(&deregType, "type", "t", "", "service type: vpn, visor, skysocks (or proxy)")
	deregisterCmd.Flags().StringVar(&deregSDURL, "sd-url", "http://sd.skycoin.com", "service discovery URL (only used with --sk)")
	deregisterCmd.Flags().StringVar(&deregNMSK, "sk", "", "secret key for signing (if not provided, uses visor RPC)")
	deregisterCmd.Flags().BoolVarP(&deregAllTypes, "all-types", "a", false, "deregister from all service types (vpn, visor, skysocks)")
	deregisterCmd.Flags().StringVarP(&deregRPCAddr, "rpc", "r", "localhost:3435", "visor RPC address (used when --sk is not provided)")
}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short:                 "Network monitor for skywire VPN and Visor.",
	Long:                  calvin.AsciiFont("network-monitor"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("failed to output build info: %v", err)
		}

		storeConfig := storeconfig.Config{
			Type: storeconfig.Memory,
		}

		s, err := store.New(storeConfig)
		if err != nil {
			log.Fatal("failed to initialize store: ", err)
		}

		mLogger := logging.NewMasterLogger()
		lvl, err := logging.LevelFromString(logLvl)
		if err != nil {
			mLogger.Fatal("invalid loglvl detected")
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
				mLogger.Infof("Starting pprof server on %s", pprofAddr)
				server := &http.Server{
					Addr:              pprofAddr,
					Handler:           pprofMux,
					ReadHeaderTimeout: 10 * time.Second,
					ReadTimeout:       30 * time.Second,
					WriteTimeout:      30 * time.Second,
					IdleTimeout:       60 * time.Second,
				}
				if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					mLogger.Errorf("pprof server failed: %v", err)
				}
			}()

			time.Sleep(100 * time.Millisecond)
		}

		var srvURLs api.ServicesURLs
		srvURLs.SD = sdURL
		srvURLs.AR = arURL
		srvURLs.UT = utURL
		srvURLs.DMSGD = dmsgdURL
		srvURLs.TPD = tpdURL

		logger := mLogger.PackageLogger("network_monitor")

		logger.WithField("addr", addr).Info("Serving Network Monitor API...")

		pubKey := cipher.PubKey{}
		pubKey.Set(pk) //nolint:errcheck,gosec
		secKey := cipher.SecKey{}
		secKey.Set(sk) //nolint:errcheck,gosec

		nmSign, _ := cipher.SignPayload([]byte(pubKey.Hex()), secKey) //nolint:errcheck

		var nmConfig api.NetworkMonitorConfig
		nmConfig.CleaningDelay = cleaningDelay
		nmConfig.PublicKey = pubKey
		nmConfig.SecretKey = secKey
		nmConfig.Signature = nmSign

		nmAPI := api.New(s, logger, srvURLs, nmConfig)

		ctx, cancel := cmdutil.SignalContext(context.Background(), logger)
		defer cancel()

		go nmAPI.InitCleaningLoop(ctx)

		go func() {
			if err := tcpproxy.ListenAndServe(addr, nmAPI); err != nil {
				logger.Errorf("serve: %v", err)
				cancel()
			}
		}()

		<-ctx.Done()
	},
}

var deregisterCmd = &cobra.Command{
	Use:   "deregister",
	Short: "Deregister service(s) from service discovery",
	Long: `
  Manually deregister one or more public keys from service discovery.

  By default, uses the local visor's RPC to perform deregistration. The visor's
  public key must be whitelisted as a network monitor in service discovery.

  Alternatively, use --sk to provide a secret key directly for signing the
  deregistration request (useful when running without a visor).

  Service types:
    - vpn      : VPN servers
    - visor    : Public visors
    - skysocks : SOCKS5 proxy servers (alias: proxy)

  Examples:
    # Deregister a VPN server using visor RPC (default)
    skywire svc nm deregister --pk <public-key> --type vpn

    # Deregister using a specific visor RPC address
    skywire svc nm deregister --pk <public-key> --type vpn --rpc localhost:3435

    # Deregister multiple keys from all service types
    skywire svc nm deregister --pk <pk1>,<pk2> --all-types

    # Deregister using a secret key directly (without visor)
    skywire svc nm deregister --pk <public-key> --type visor --sk <secret-key>

    # Deregister from a specific service discovery instance (with --sk)
    skywire svc nm deregister --pk <public-key> --type visor --sk <secret-key> --sd-url http://localhost:9098`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(_ *cobra.Command, _ []string) {
		// Validate required flags
		if deregPK == "" {
			log.Fatal("--pk is required: specify the public key(s) to deregister")
		}
		if deregType == "" && !deregAllTypes {
			log.Fatal("--type or --all-types is required: specify the service type(s)")
		}

		// Parse public keys to deregister
		pkStrings := strings.Split(deregPK, ",")
		var pks []cipher.PubKey
		for _, pkStr := range pkStrings {
			pkStr = strings.TrimSpace(pkStr)
			if pkStr == "" {
				continue
			}
			var pk cipher.PubKey
			if err := pk.Set(pkStr); err != nil {
				log.Fatalf("invalid public key %q: %v", pkStr, err)
			}
			pks = append(pks, pk)
		}
		if len(pks) == 0 {
			log.Fatal("no valid public keys provided")
		}

		// Determine service types to deregister from
		var serviceTypes []string
		if deregAllTypes {
			serviceTypes = []string{"vpn", "visor", "skysocks"}
		} else {
			// Normalize service type
			sType := strings.ToLower(strings.TrimSpace(deregType))
			if sType == "proxy" {
				sType = "skysocks"
			}
			if sType != "vpn" && sType != "visor" && sType != "skysocks" {
				log.Fatalf("invalid service type %q: must be vpn, visor, or skysocks (proxy)", deregType)
			}
			serviceTypes = []string{sType}
		}

		// Use visor RPC or direct signing based on whether --sk is provided
		if deregNMSK != "" {
			// Direct signing mode - use the provided secret key
			var nmSecKey cipher.SecKey
			if err := nmSecKey.Set(deregNMSK); err != nil {
				log.Fatalf("invalid secret key: %v", err)
			}

			// Derive public key from secret key
			nmPubKey, err := nmSecKey.PubKey()
			if err != nil {
				log.Fatalf("failed to derive public key from secret key: %v", err)
			}

			// Generate signature (sign the public key hex with the secret key)
			nmSign, err := cipher.SignPayload([]byte(nmPubKey.Hex()), nmSecKey)
			if err != nil {
				log.Fatalf("failed to sign payload: %v", err)
			}

			// Convert pks to string slice for direct API call
			pkStrs := make([]string, len(pks))
			for i, pk := range pks {
				pkStrs[i] = pk.Hex()
			}

			// Perform deregistration for each service type
			for _, sType := range serviceTypes {
				if err := deregisterFromSD(deregSDURL, sType, pkStrs, nmPubKey, nmSign); err != nil {
					log.Printf("failed to deregister from %s: %v", sType, err)
				} else {
					fmt.Printf("Successfully deregistered %d key(s) from %s\n", len(pks), sType)
				}
			}
		} else {
			// Visor RPC mode - connect to the visor and use its keys
			logger := logging.MustGetLogger("nm_deregister")
			const rpcDialTimeout = time.Second * 5
			conn, err := net.DialTimeout("tcp", deregRPCAddr, rpcDialTimeout)
			if err != nil {
				log.Fatalf("RPC connection failed; is skywire running?: %v", err)
			}
			rpcClient := visor.NewRPCClient(logger, conn, visor.RPCPrefix, 30*time.Second)
			defer rpcClient.Close() //nolint:errcheck

			// Perform deregistration for each service type
			for _, sType := range serviceTypes {
				if err := rpcClient.DeregisterService(pks, sType); err != nil {
					log.Printf("failed to deregister from %s: %v", sType, err)
				} else {
					fmt.Printf("Successfully deregistered %d key(s) from %s\n", len(pks), sType)
				}
			}
		}
	},
}

// deregisterFromSD sends a deregister request to service discovery
func deregisterFromSD(sdURL, serviceType string, pks []string, nmPK cipher.PubKey, nmSign cipher.Sig) error {
	// Build the URL
	reqURL, err := url.Parse(fmt.Sprintf("%s/api/services/deregister/%s", sdURL, serviceType))
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Marshal the public keys to JSON
	jsonData, err := json.Marshal(pks)
	if err != nil {
		return fmt.Errorf("failed to marshal public keys: %w", err)
	}

	// Create the request
	req, err := http.NewRequest(http.MethodDelete, reqURL.String(), bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("NM-PK", nmPK.Hex())
	req.Header.Set("NM-Sign", nmSign.Hex())

	// Send the request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	// Check response
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("failed to execute command: ", err)
	}
}
