// Package commands cmd/transport-setup/commands/root.go
package commands

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/google/uuid"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	"github.com/skycoin/skywire-services/pkg/transport-setup/api"
	"github.com/skycoin/skywire-services/pkg/transport-setup/config"
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

func init() {
	RootCmd.Flags().SortFlags = false
	addTPCmd.Flags().SortFlags = false
	rmTPCmd.Flags().SortFlags = false
	listTPCmd.Flags().SortFlags = false
	RootCmd.Flags().StringVarP(&configFile, "config", "c", "", "path to config file\033[0m")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "debug", "[info|error|warn|debug|trace|panic]\033[0m")
	RootCmd.AddCommand(addTPCmd, rmTPCmd, listTPCmd)
	addTPCmd.Flags().StringVarP(&fromPK, "from", "1", "", "PK to request transport setup\033[0m")
	addTPCmd.Flags().StringVarP(&toPK, "to", "2", "", "other transport edge PK\033[0m")
	addTPCmd.Flags().StringVarP(&tpType, "type", "t", "", "transport type to request creation of [stcpr|sudph|dmsg]\033[0m")
	rmTPCmd.Flags().StringVarP(&fromPK, "from", "1", "", "PK to request transport takedown\033[0m")
	rmTPCmd.Flags().StringVarP(&tpID, "tpid", "i", "", "id of transport to remove\033[0m")
	listTPCmd.Flags().StringVarP(&fromPK, "from", "1", "", "PK to request transport list\033[0m")
	addTPCmd.Flags().BoolVarP(&nice, "pretty", "p", false, "pretty print result\033[0m")
	rmTPCmd.Flags().BoolVarP(&nice, "pretty", "p", false, "pretty print result\033[0m")
	listTPCmd.Flags().BoolVarP(&nice, "pretty", "p", false, "pretty print result\033[0m")
	addTPCmd.Flags().StringVarP(&tpsnAddr, "addr", "z", "http://127.0.0.1:8080", "address of the transport setup-node\033[0m")
	rmTPCmd.Flags().StringVarP(&tpsnAddr, "addr", "z", "http://127.0.0.1:8080", "address of the transport setup-node\033[0m")
	listTPCmd.Flags().StringVarP(&tpsnAddr, "addr", "z", "http://127.0.0.1:8080", "address of the transport setup-node\033[0m")
}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Transport setup server for skywire",
	Long: `
	┌┬┐┬─┐┌─┐┌┐┌┌─┐┌─┐┌─┐┬─┐┌┬┐  ┌─┐┌─┐┌┬┐┬ ┬┌─┐
	 │ ├┬┘├─┤│││└─┐├─┘│ │├┬┘ │───└─┐├┤  │ │ │├─┘
	 ┴ ┴└─┴ ┴┘└┘└─┘┴  └─┘┴└─ ┴   └─┘└─┘ ┴ └─┘┴

Transport setup server for skywire
Takes config in the following format:
{
    "dmsg": {
        "discovery": "http://dmsgd.skywire.skycoin.com",
        "servers": [],
        "sessions_count": 2
    },
    "log_level": "",
    "port":8080,
    "public_key": "",
    "secret_key": "",
    "transport_discovery": "http://tpd.skywire.skycoin.com"
}`,
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
		log := logging.MustGetLogger(loggerTag)
		lvl, err := logging.LevelFromString(logLvl)
		if err != nil {
			log.Fatal("Invalid loglvl detected")
		}
		logging.SetLevel(lvl)

		conf := config.MustReadConfig(configFile, log)
		api := api.New(log, conf)
		srv := &http.Server{
			Addr:              fmt.Sprintf(":%d", conf.Port),
			ReadHeaderTimeout: 2 * time.Second,
			IdleTimeout:       30 * time.Second,
			Handler:           api,
		}
		if err := srv.ListenAndServe(); err != nil {
			log.Errorf("ListenAndServe: %v", err)
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
