// Package cliservices cmd/skywire-cli/commands/services/root.go
package cliservices

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	path string
)

func init() {
	servicesCmd.Flags().SortFlags = false
	servicesCmd.Flags().StringVarP(&path, "path", "p", "/opt/skywire/services-config.json", "path of services-config file, default is for pkg installation")
}

// RootCmd is servicesCmd
var RootCmd = servicesCmd

var servicesCmd = &cobra.Command{
	Use:   "services update",
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

type servicesConf struct { //nolint
	Test visorconfig.Services `json:"test"`
	Prod visorconfig.Services `json:"prod"`
}

func fetchServicesConf() (servicesConf, error) {
	var newConf servicesConf
	var prodConf visorconfig.Services
	prodResp, err := http.Get(skyenv.ServiceConfAddr)
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
	testResp, err := http.Get(skyenv.TestServiceConfAddr)
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
