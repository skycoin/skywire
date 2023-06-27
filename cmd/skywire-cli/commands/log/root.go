// Package clilog cmd/skywire-cli/commands/log/root.go
package clilog

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/hashicorp/go-version"
	"github.com/skycoin/skywire/pkg/dmsgget"
	"github.com/skycoin/skywire/pkg/dmsghttp"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/skyenv"
)

var (
	env            string
	duration       int
	minv           string
	allVisors      bool
	batchSize      int
	maxFileSize    int64
	utAddr         string
	sk             cipher.SecKey
	dmsgDisc       string
	logOnly        bool
	surveyOnly     bool
	deleteOnErrors bool
)

func init() {
	logCmd.Flags().SortFlags = false
	logCmd.Flags().StringVarP(&env, "env", "e", "prod", "selecting env to fetch uptimes, default is prod")
	logCmd.Flags().BoolVarP(&logOnly, "log", "l", false, "fetch only transport logs")
	logCmd.Flags().BoolVarP(&surveyOnly, "survey", "v", false, "fetch only surveys")
	logCmd.Flags().BoolVarP(&deleteOnErrors, "clean", "c", false, "delete files and folders on errors")
	logCmd.Flags().StringVar(&minv, "minv", "v1.3.4", "minimum version for get logs, default is 1.3.4")
	logCmd.Flags().IntVarP(&duration, "duration", "n", 1, "numberof days before today to fetch transport logs for")
	logCmd.Flags().BoolVar(&allVisors, "all", false, "consider all visors ; no version filtering")
	logCmd.Flags().IntVar(&batchSize, "batchSize", 50, "number of visor in each batch, default is 50")
	logCmd.Flags().Int64Var(&maxFileSize, "maxfilesize", 30, "maximum file size allowed to download during collecting logs, in KB")
	logCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "D", skyenv.DmsgDiscAddr, "dmsg discovery url\n")
	logCmd.Flags().StringVarP(&utAddr, "ut", "u", "", "custom uptime tracker url")
	if os.Getenv("DMSGGET_SK") != "" {
		sk.Set(os.Getenv("DMSGGET_SK")) //nolint
	}
	logCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
}

// RootCmd is surveyCmd
var RootCmd = logCmd

var logCmd = &cobra.Command{
	Use:   "log",
	Short: "survey & transport log collection",
	Long:  "collect surveys and transport logging from visors which are online in the uptime tracker",
	Run: func(cmd *cobra.Command, args []string) {
		log := logging.MustGetLogger("log-collecting")
		if logOnly && surveyOnly {
			log.Fatal("use of mutually exclusive flags --log and --survey")
		}

		// Preparing directories
		if _, err := os.ReadDir("log_collecting"); err != nil {
			if err := os.Mkdir("log_collecting", 0750); err != nil {
				log.Panic("Unable to log_collecting directory")
			}
		}

		if err := os.Chdir("log_collecting"); err != nil {
			log.Panic("Unable to change directory to log_collecting")
		}

		// Create dmsgget instance
		dg := dmsgget.New(flag.CommandLine)
		flag.Parse()

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()
		go func() {
			<-ctx.Done()
			cancel()
			os.Exit(1)
		}()
		// Set the uptime tracker to fetch data from
		endpoint := skyenv.UptimeTrackerAddr + "/uptimes?v=v2"
		if env == "test" {
			endpoint = skyenv.TestUptimeTrackerAddr + "/uptimes?v=v2"
		}
		if utAddr != "" {
			endpoint = utAddr
		}
		//Fetch the uptime data over http
		uptimes, err := getUptimes(endpoint, log)
		if err != nil {
			log.WithError(err).Panic("Unable to get data from uptime tracker.")
		}
		//randomize the order of the survey collection - workaround for hanging
		rand.Shuffle(len(uptimes), func(i, j int) {
			uptimes[i], uptimes[j] = uptimes[j], uptimes[i]
		})
		// Create dmsg http client
		pk, err := sk.PubKey()
		if err != nil {
			pk, sk = cipher.GenerateKeyPair()
		}

		dmsgC, closeDmsg, err := dg.StartDmsg(ctx, log, pk, sk)
		if err != nil {
			log.WithError(err).Panic(err)
		}
		defer closeDmsg()

		// Connect dmsgC to all servers
		allServer := getAllDMSGServers()
		for _, server := range allServer {
			dmsgC.EnsureAndObtainSession(ctx, server.PK) //nolint
		}

		minimumVersion, _ := version.NewVersion(minv) //nolint

		start := time.Now()
		var bulkFolders []string
		// Get visors data
		var wg sync.WaitGroup
		for _, v := range uptimes {
			if v.Online {

				visorVersion, err := version.NewVersion(v.Version) //nolint
				if err != nil {
					log.Warnf("The version %s for visor %s is not valid", v.Version, v.PubKey)
					continue
				}
				if !allVisors && visorVersion.LessThan(minimumVersion) {
					log.Warnf("The version %s for visor %s does not satisfy our minimum version condition", v.Version, v.PubKey)
					continue
				}
				wg.Add(1)
				go func(key string, wg *sync.WaitGroup) {
					httpC := http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC), Timeout: 10 * time.Second}
					defer httpC.CloseIdleConnections()
					defer wg.Done()

					deleteOnError := false
					if _, err := os.ReadDir(key); err != nil {
						if err := os.Mkdir(key, 0750); err != nil {
							log.Panicf("Unable to create directory for visor %s", key)
						}
						deleteOnError = true
					}
					// health check before downloading anything else
					// delete that folder if the health check fails
					err = download(ctx, log, httpC, "health", "health.json", key, maxFileSize)
					if err != nil {
						if deleteOnErrors {
							if deleteOnError {
								bulkFolders = append(bulkFolders, key)
							}
							return
						}
					}
					if !logOnly {
						download(ctx, log, httpC, "node-info.json", "node-info.json", key, maxFileSize) //nolint
					}
					if !surveyOnly {
						if duration == 1 {
							yesterday := time.Now().AddDate(0, 0, -1).UTC().Format("2006-01-02")
							download(ctx, log, httpC, "transport_logs/"+yesterday+".csv", yesterday+".csv", key, maxFileSize) //nolint
						} else {
							for i := 1; i <= duration; i++ {
								date := time.Now().AddDate(0, 0, -i).UTC().Format("2006-01-02")
								download(ctx, log, httpC, "transport_logs/"+date+".csv", date+".csv", key, maxFileSize) //nolint
							}
						}
					}
				}(v.PubKey, &wg)
				batchSize--
				if batchSize == 0 {
					time.Sleep(15 * time.Second)
					batchSize = 50
				}
			}
		}

		wg.Wait()
		for _, key := range bulkFolders {
			if err := os.RemoveAll(key); err != nil {
				log.Warnf("Unable to remove directory %s", key)
			}
		}
		log.Infof("Process Duration: %s", time.Since(start))
	},
}

func download(ctx context.Context, log *logging.Logger, httpC http.Client, targetPath, fileName, pubkey string, maxSize int64) error {
	target := fmt.Sprintf("dmsg://%s:80/%s", pubkey, targetPath)
	file, _ := os.Create(pubkey + "/" + fileName) //nolint
	defer file.Close()                            //nolint

	if err := dmsgget.Download(ctx, log, &httpC, file, target, maxSize); err != nil {
		log.WithError(err).Errorf("The %s for visor %s not available", fileName, pubkey)
		return err
	}
	return nil
}

func getUptimes(endpoint string, log *logging.Logger) ([]VisorUptimeResponse, error) {
	var results []VisorUptimeResponse

	response, err := http.Get(endpoint) //nolint
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
	PubKey     string  `json:"pk"`
	Uptime     float64 `json:"up"`
	Downtime   float64 `json:"down"`
	Percentage float64 `json:"pct"`
	Online     bool    `json:"on"`
	Version    string  `json:"version"`
}

func getAllDMSGServers() []dmsgServer {
	var results []dmsgServer

	response, err := http.Get(dmsgDisc + "/dmsg-discovery/all_servers") //nolint
	if err != nil {
		return results
	}

	defer response.Body.Close() //nolint
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return results
	}
	err = json.Unmarshal(body, &results)
	if err != nil {
		return results
	}
	return results
}

type dmsgServer struct {
	PK cipher.PubKey `json:"static"`
}
