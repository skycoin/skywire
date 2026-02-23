// Package commands cmd/transport-setup/commands/root.go
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/bitfield/script"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport-setup/api"
	"github.com/skycoin/skywire/pkg/transport-setup/config"
)

var (
	pk1        cipher.PubKey
	pk2        cipher.PubKey
	logLvl     string
	configFile string
	tpsnAddr   string
	fromPK     string
	toPK       string
	tpID       string
	tpType     string
	nice       bool
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
	exPK1 := "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"
	exPK2 := "03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"
	exTPID := "e7a7f1b3-c040-47f8-9e12-a0a1459b3456"

	return fmt.Sprintf(`
Request/Response Examples:

POST /add
  Request:  %s
  Response: {"id": "%s", "type": "stcpr", ...}

POST /remove
  Request:  %s
  Response: {"success": true}

GET /{pk}/transports
  %s`,
		exampleJSON(api.TransportRequest{Type: "stcpr"}),
		exTPID,
		exampleJSON(api.UUIDRequest{}),
		exampleJSON([]map[string]interface{}{{
			"id": exTPID, "type": "stcpr",
			"edges": []string{exPK1, exPK2},
		}}),
	)
}

func init() {
	RootCmd.Flags().SortFlags = false
	addTPCmd.Flags().SortFlags = false
	rmTPCmd.Flags().SortFlags = false
	listTPCmd.Flags().SortFlags = false
	RootCmd.Flags().StringVarP(&configFile, "config", "c", "", "path to config file")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "debug", "[info|error|warn|debug|trace|panic]\n\r")
	RootCmd.AddCommand(addTPCmd, rmTPCmd, listTPCmd)
	addTPCmd.Flags().StringVarP(&fromPK, "from", "1", "", "PK to request transport setup")
	addTPCmd.Flags().StringVarP(&toPK, "to", "2", "", "other transport edge PK")
	addTPCmd.Flags().StringVarP(&tpType, "type", "t", "", "transport type to request creation of [stcpr|sudph|dmsg]")
	rmTPCmd.Flags().StringVarP(&fromPK, "from", "1", "", "PK to request transport takedown")
	rmTPCmd.Flags().StringVarP(&tpID, "tpid", "i", "", "id of transport to remove")
	listTPCmd.Flags().StringVarP(&fromPK, "from", "1", "", "PK to request transport list")
	addTPCmd.Flags().BoolVarP(&nice, "pretty", "p", false, "pretty print result")
	rmTPCmd.Flags().BoolVarP(&nice, "pretty", "p", false, "pretty print result")
	listTPCmd.Flags().BoolVarP(&nice, "pretty", "p", false, "pretty print result")
	addTPCmd.Flags().StringVarP(&tpsnAddr, "addr", "z", "http://127.0.0.1:8080", "address of the transport setup-node\n\r")
	rmTPCmd.Flags().StringVarP(&tpsnAddr, "addr", "z", "http://127.0.0.1:8080", "address of the transport setup-node\n\r")
	listTPCmd.Flags().StringVarP(&tpsnAddr, "addr", "z", "http://127.0.0.1:8080", "address of the transport setup-node\n\r")
}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Transport setup server for skywire",
	Long: calvin.AsciiFont("transport-setup") + `
Transport Setup Node - remotely manages transports on visors via dmsg RPC.

HTTP Endpoints:
  POST /{pk}/transports    List transports on visor
  POST /add                Add transport between two visors
  POST /remove             Remove transport from visor
` + generateExamples() + `

Example Config:
` + exampleJSON(config.Config{
		Port: 8080,
	}) + `

Generate Keys:
  skywire cli config gen-keys | tee tps-keys.txt
  # Line 1: public_key, Line 2: secret_key`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		if configFile == "" {
			log.Fatal("please specify config file")
		}
		const loggerTag = "transport_setup"
		logger := logging.MustGetLogger(loggerTag)
		lvl, err := logging.LevelFromString(logLvl)
		if err != nil {
			logger.Fatal("Invalid loglvl detected")
		}
		logging.SetLevel(lvl)

		conf := config.MustReadConfig(configFile, logger)
		tpsAPI := api.New(logger, conf)

		// Setup context with signal handling
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			logger.Info("Received shutdown signal")
			cancel()
		}()

		// Start dmsg listener in goroutine
		go func() {
			logger.Info("Starting dmsg RPC listener")
			if err := tpsAPI.ServeDmsg(ctx); err != nil {
				if ctx.Err() == nil {
					logger.WithError(err).Error("Dmsg server error")
				}
			}
		}()

		// Start HTTP server
		srv := &http.Server{
			Addr:              fmt.Sprintf(":%d", conf.Port),
			ReadHeaderTimeout: 2 * time.Second,
			IdleTimeout:       30 * time.Second,
			Handler:           tpsAPI,
		}

		logger.WithField("addr", srv.Addr).Info("Starting HTTP server")

		// Graceful shutdown
		go func() {
			<-ctx.Done()
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer shutdownCancel()
			if err := srv.Shutdown(shutdownCtx); err != nil {
				logger.WithError(err).Error("HTTP server shutdown error")
			}
		}()

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Errorf("ListenAndServe: %v", err)
		}
	},
}

var addTPCmd = &cobra.Command{
	Use:                   "add",
	Short:                 "add transport to remote visor",
	Long:                  ``,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(_ *cobra.Command, _ []string) {
		err := pk1.Set(fromPK)
		if err != nil {
			log.Fatalf("-1 invalid public key: %v\n", err)
		}
		err = pk2.Set(toPK)
		if err != nil {
			log.Fatalf("-2 invalid public key: %v\n", err)
		}
		if tpType != "dmsg" && tpType != "stcpr" && tpType != "sudph" {
			log.Fatal("invalid transport type specified: ", tpType)
		}
		addtp := api.TransportRequest{
			From: pk1,
			To:   pk2,
			Type: tpType,
		}
		addtpJSON, err := json.Marshal(addtp)
		if err != nil {
			log.Fatalf("Error occurred: %v\n", err)
		}
		res, err := script.Echo(string(addtpJSON)).Post(tpsnAddr + "/add").String()
		if err != nil {
			log.Fatalf("Error occurred: %v\n", err)
		}
		if nice {
			fmt.Printf("%v", string(pretty.Color(pretty.Pretty([]byte(res)), nil)))
		} else {
			fmt.Printf("%v", res)
		}

	},
}
var rmTPCmd = &cobra.Command{
	Use:                   "rm",
	Short:                 "remove transport from remote visor",
	Long:                  ``,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(_ *cobra.Command, _ []string) {
		err := pk1.Set(fromPK)
		if err != nil {
			log.Fatalf("invalid public key: %v\n", err)
		}
		tpid, err := uuid.Parse(tpID)
		if err != nil {
			log.Fatalf("invalid tp id: %v\n", err)
		}
		rmtp := api.UUIDRequest{
			From: pk1,
			ID:   tpid,
		}
		rmtpJSON, err := json.Marshal(rmtp)
		if err != nil {
			log.Fatalf("Error occurred: %v\n", err)
		}
		res, err := script.Echo(string(rmtpJSON)).Post(tpsnAddr + "/remove").String()
		if err != nil {
			log.Fatalf("Error occurred: %v\n", err)
		}
		if nice {
			fmt.Printf("%v", string(pretty.Color(pretty.Pretty([]byte(res)), nil)))
		} else {
			fmt.Printf("%v", res)
		}
	},
}
var listTPCmd = &cobra.Command{
	Use:                   "list",
	Short:                 "list transports of remote visor",
	Long:                  ``,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(_ *cobra.Command, _ []string) {
		res, err := script.Get(tpsnAddr + "/" + fromPK + "/transports").String()
		if err != nil {
			log.Fatal("something unexpected happened: ", err, res)
		}
		if nice {
			fmt.Printf("%v", string(pretty.Color(pretty.Pretty([]byte(res)), nil)))
		} else {
			fmt.Printf("%v", res)
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
