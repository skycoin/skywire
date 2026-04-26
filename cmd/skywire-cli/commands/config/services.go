// Package cliconfig cmd/skywire-cli/commands/config/services.go
package cliconfig

import (
	"context"
	"encoding/json"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func init() {
	updateCmd.AddCommand(servicesCmd)
	servicesCmd.Flags().SortFlags = false
	//TODO: fix path for non linux package defaults
	servicesCmd.Flags().StringVarP(&path, "path", "p", "/opt/skywire/services-config.json", "path of services-config file, default is for pkg installation")
	servicesCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")
	clirpc.RegisterFetchFlags(servicesCmd)
}

var servicesCmd = &cobra.Command{
	Use:   "svc",
	Short: "update services-config.json file from config bootstrap service",
	Run: func(cmd *cobra.Command, _ []string) {
		log := logging.MustGetLogger("services_updater")

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()
		go func() {
			<-ctx.Done()
			cancel()
			os.Exit(1)
		}()

		servicesConf, err := fetchServicesConf(cmd.Flags())
		if err != nil {
			log.WithError(err).Error("Cannot fetching updated services-config data")
		}

		file, err := json.MarshalIndent(servicesConf, "", " ")
		if err != nil {
			log.WithError(err).Error("Error accurs during marshal content to json file")
		}

		err = os.WriteFile(path, file, 0600)
		if err != nil {
			log.WithError(err).Errorf("Cannot save new services-config.json file at %s", path)
		}
	},
}

func fetchServicesConf(cmdFlags *pflag.FlagSet) (servicesConf, error) {
	var newConf servicesConf

	// Fetch prod config via visor RPC → DMSG → HTTP fallback.
	prodBody, err := clirpc.FetchServiceURL(cmdFlags, serviceConfURL)
	if err != nil {
		return newConf, err
	}
	var prodConf visorconfig.Services
	if err := json.Unmarshal(prodBody, &prodConf); err != nil {
		return newConf, err
	}
	newConf.Prod = prodConf

	// Fetch test config via the same chain.
	testBody, err := clirpc.FetchServiceURL(cmdFlags, testServiceConfURL)
	if err != nil {
		return newConf, err
	}
	var testConf visorconfig.Services
	if err := json.Unmarshal(testBody, &testConf); err != nil {
		return newConf, err
	}
	newConf.Test = testConf
	return newConf, nil
}
