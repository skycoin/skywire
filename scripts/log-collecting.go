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
	"sync"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"

	"github.com/skycoin/dmsg/pkg/dmsgget"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
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

	// Create dmsg http client
	pk, sk, _ := genKeys("") //nolint
	dmsgC, closeDmsg, err := dg.StartDmsg(ctx, log, pk, sk)
	if err != nil {
		log.WithError(err).Panic(err)
	}
	defer closeDmsg()

	httpC := http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}

	// Get visors data
	var wg sync.WaitGroup
	for _, v := range uptimes {
		wg.Add(1)
		go func(key string, wg *sync.WaitGroup) {
			defer wg.Done()
			var infoErr, shaErr error

			if err := os.Mkdir(key, 0750); err != nil {
				log.Panicf("Unable to create directory for visor %s", key)
			}

			infoErr = download(ctx, log, httpC, "node-info.json", key)
			shaErr = download(ctx, log, httpC, "node-info.sha", key)
			yesterday := time.Now().AddDate(0, 0, -1).UTC().Format("2006-01-02")
			download(ctx, log, httpC, "transport_logs/"+yesterday+".csv", key) //nolint

			if shaErr != nil || infoErr != nil {
				if err := os.RemoveAll(key); err != nil {
					log.Warnf("Unable to remove directory %s", key)
				}
			}
		}(v.PubKey, &wg)
	}

	wg.Wait()
}

func download(ctx context.Context, log *logging.Logger, httpC http.Client, fileName, pubkey string) error {
	target := fmt.Sprintf("dmsg://%s:80/%s", pubkey, fileName)
	file, _ := os.Create(pubkey + "/" + fileName) //nolint
	defer file.Close()                            //nolint

	if err := dmsgget.Download(ctx, log, &httpC, file, target); err != nil {
		log.WithError(err).Errorf("The %s for visor %s not available", fileName, pubkey)
		return err
	}
	return nil
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

func genKeys(skStr string) (pk cipher.PubKey, sk cipher.SecKey, err error) {
	if skStr == "" {
		pk, sk = cipher.GenerateKeyPair()
		return
	}
	if err = sk.Set(skStr); err != nil {
		return
	}
	pk, err = sk.PubKey()
	return
}
