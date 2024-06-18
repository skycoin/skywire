// Package cliconfig cmd/skywire-cli/commands/config/dmsghttp.go
package cliconfig

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/skyenv"
)

var dmsghttpPath string

func init() {
	updateCmd.AddCommand(dmsghttpCmd)
	dmsghttpCmd.Flags().SortFlags = false
	//TODO: fix path for non linux package defaults
	dmsghttpCmd.Flags().StringVarP(&dmsghttpPath, "path", "p", "/opt/skywire/dmsghttp-config.json", "path of dmsghttp-config file, default is for pkg installation")
}

var dmsghttpCmd = &cobra.Command{
	Use:   "dmsghttp",
	Short: "update dmsghttp-config.json file from config bootstrap service",
	Run: func(cmd *cobra.Command, args []string) { //nolint
		log := logging.MustGetLogger("dmsghttp_updater")

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()
		go func() {
			<-ctx.Done()
			cancel()
			os.Exit(1)
		}()
		dmsghttpConf, err := fetchDmsghttpConf()
		if err != nil {
			log.WithError(err).Error("Cannot fetching updated dmsghttp-config data")
		}

		file, err := json.MarshalIndent(dmsghttpConf, "", " ")
		if err != nil {
			log.WithError(err).Error("Error accurs during marshal content to json file")
		}

		err = os.WriteFile(dmsghttpPath, file, 0600)
		if err != nil {
			log.WithError(err).Errorf("Cannot save new dmsghttp-config.json file at %s", dmsghttpPath)
		}
	},
}

type dmsghttpConf struct { //nolint
	Test httputil.DMSGHTTPConf `json:"test"`
	Prod httputil.DMSGHTTPConf `json:"prod"`
}

func fetchDmsghttpConf() (dmsghttpConf, error) {
	var newConf dmsghttpConf
	var prodConf httputil.DMSGHTTPConf
	prodResp, err := http.Get(skyenv.ServiceConfAddr + "/dmsghttp")
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

	var testConf httputil.DMSGHTTPConf
	testResp, err := http.Get(skyenv.TestServiceConfAddr + "/dmsghttp")
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
