// Package main in scripts folder
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"

	"github.com/skycoin/dmsg/pkg/dmsgget"
)

func main() {
	log := logging.MustGetLogger("log-collecting")

	// Preparing directories
	if _, err := os.ReadDir("log_collecting"); err == nil {
		if err := os.RemoveAll("log_collecting"); err != nil {
			log.Panic("Unable to remove old log_collecting directory")
		}
	}
	if err := os.Mkdir("log_collecting", 0750); err != nil {
		log.Panic("Unable to log_collecting directory")
	}
	if err := os.Chdir("log_collecting"); err != nil {
		log.Panic("Unable to change directory to log_collecting")
	}

	// Create dmsgget instance
	dg := dmsgget.New(flag.CommandLine)
	flag.Parse()

	ctx, cancel := cmdutil.SignalContext(context.Background(), log)
	defer cancel()

	// Fetch visors data from uptime tracker
	uptimes, err := getUptimes(log)
	if err != nil {
		log.WithError(err).Panic("Unable to get data from uptime tracker.")
	}

	// Get visors data
	for _, v := range uptimes {
		var infoErr, shaErr error

		if err := os.Mkdir(v.PubKey, 0750); err != nil {
			log.Panicf("Unable to create directory for visor %s", v.PubKey)
		}
		if err := os.Chdir(v.PubKey); err != nil {
			log.Panicf("Unable to change directory to %s", v.PubKey)
		}

		nodeInfo := []string{fmt.Sprintf("dmsg://%s:80/node-info.json", v.PubKey)}
		if infoErr = dg.Run(ctx, log, "", nodeInfo); infoErr != nil {
			log.WithError(infoErr).Errorf("node-info.json for visor %s not available", v.PubKey)
		}

		nodeInfoSha := []string{fmt.Sprintf("dmsg://%s:80/node-info.sha", v.PubKey)}
		if shaErr = dg.Run(ctx, log, "", nodeInfoSha); shaErr != nil {
			log.WithError(shaErr).Errorf("node-info.sha for visor %s not available", v.PubKey)
		}

		if err := os.Chdir(".."); err != nil {
			log.Panic("Unable to change directory to root")
		}

		if shaErr != nil || infoErr != nil {
			if err := os.RemoveAll(v.PubKey); err != nil {
				log.Warnf("Unable to remove directory %s", v.PubKey)
			}
		}
	}
}

func getUptimes(log *logging.Logger) ([]VisorUptimeResponse, error) {
	endpoint := "https://ut.skywire.dev/uptimes"
	var results []VisorUptimeResponse

	response, err := http.Get(endpoint)
	if err != nil {
		log.Error("Error while fetching data from uptime service. Error: ", err)
		return results, errors.New("Cannot get Uptime data")
	}

	defer response.Body.Close() //nolint
	body, err := io.ReadAll(response.Body)
	if err != nil {
		log.Error("Error while reading data from uptime service. Error: ", err)
		return results, errors.New("Cannot get Uptime data")
	}
	log.Debugf("Successfully  called uptime service and received answer %+v", results)
	err = json.Unmarshal(body, &results)
	if err != nil {
		log.Errorf("Error while unmarshalling data from uptime service.\nBody:\n%v\nError:\n%v ", string(body), err)
		return results, errors.New("Cannot get Uptime data")
	}
	return results, nil
}

type VisorUptimeResponse struct { //nolint
	PubKey     string  `json:"key"`
	Uptime     float64 `json:"uptime"`
	Downtime   float64 `json:"downtime"`
	Percentage float64 `json:"percentage"`
	Online     bool    `json:"online"`
}
