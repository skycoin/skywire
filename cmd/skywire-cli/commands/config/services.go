// Package cliconfig cmd/skywire-cli/commands/config/services.go
package cliconfig

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func init() {
	updateCmd.AddCommand(servicesCmd)
	servicesCmd.Flags().SortFlags = false
	//TODO: fix path for non linux package defaults
	servicesCmd.Flags().StringVarP(&path, "path", "p", "/opt/skywire/services-config.json", "path of services-config file, default is for pkg installation")
}

var servicesCmd = &cobra.Command{
	Use:   "svc",
	Short: "update services-config.json file from config bootstrap service",
	Run: func(cmd *cobra.Command, args []string) {
		log := logging.MustGetLogger("services_updater")

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()
		go func() {
			<-ctx.Done()
			cancel()
			os.Exit(1)
		}()

		servicesConf, err := fetchServicesConf()
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

func fetchServicesConf() (servicesConf, error) {
	var newConf servicesConf
	var prodConf visorconfig.Services
	prodResp, err := http.Get(serviceConfURL)
	if err != nil {
		return newConf, err
	}
	defer prodResp.Body.Close() //nolint
	body, err := io.ReadAll(prodResp.Body)
	if err != nil {
		return newConf, err
	}
	err = json.Unmarshal(body, &prodConf)
	if err != nil {
		return newConf, err
	}
	newConf.Prod = prodConf

	var testConf visorconfig.Services
	testResp, err := http.Get(testServiceConfURL)
	if err != nil {
		return newConf, err
	}
	defer testResp.Body.Close() //nolint
	body, err = io.ReadAll(testResp.Body)
	if err != nil {
		return newConf, err
	}
	err = json.Unmarshal(body, &testConf)
	if err != nil {
		return newConf, err
	}
	newConf.Test = testConf
	return newConf, nil
}
