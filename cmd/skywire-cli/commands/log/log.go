// Package clilog cmd/skywire-cli/commands/log/log.go
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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/go-version"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/pkg/dmsgcurl"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
)

func init() {
	log := logging.MustGetLogger("skywire-cli")
	var envServices skywire.EnvServices
	var services skywire.Services
	if err := json.Unmarshal([]byte(jsonData), &envServices); err == nil {
		if err := json.Unmarshal(envServices.Prod, &services); err == nil {
			dmsgDiscURL = services.DmsgDiscovery
			utURL = services.UptimeTracker
		}
	}

	logCmd.Flags().SortFlags = false
	logCmd.Flags().BoolVarP(&logOnly, "log", "l", false, "fetch only transport logs")
	logCmd.Flags().BoolVarP(&surveyOnly, "survey", "v", false, "fetch only surveys")
	logCmd.Flags().StringVarP(&fetchFile, "file", "f", "", "fetch only a specific file from all online visors")
	logCmd.Flags().StringVarP(&fetchFrom, "pks", "k", "", "fetch only from specific public keys ; semicolon separated")
	logCmd.Flags().StringVarP(&writeDir, "dir", "d", "log_collecting", "save files to specified dir")
	logCmd.Flags().BoolVarP(&deleteOnErrors, "clean", "c", false, "delete files and folders on errors")
	logCmd.Flags().StringVar(&minv, "minv", "v1.3.19", "minimum visor version to fetch from")
	logCmd.Flags().StringVar(&incVer, "include-versions", "", "list of version that not satisfy our minimum version condition, but we want include them")
	logCmd.Flags().IntVarP(&duration, "duration", "n", 0, "number of days before today to fetch transport logs for")
	logCmd.Flags().BoolVar(&allVisors, "all", false, "consider all visors ; no version filtering")
	logCmd.Flags().IntVar(&batchSize, "batchSize", 50, "number of visor in each batch")
	logCmd.Flags().Int64Var(&maxFileSize, "maxfilesize", 1024, "maximum file size allowed to download during collecting logs, in KB")
	logCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "D", dmsgDiscURL, "dmsg discovery url\n")
	logCmd.Flags().StringVarP(&utAddr, "ut", "u", utURL, "uptime tracker url\n")
	if os.Getenv("DMSGCURL_SK") != "" {
		sk.Set(os.Getenv("DMSGCURL_SK")) //nolint
	}
	logCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
}

var logCmd = &cobra.Command{
	Use:   "log",
	Short: "survey & transport log collection",
	Long:  "Fetch health, survey, and transport logging from visors which are online in the uptime tracker\nhttp://ut.skywire.skycoin.com/uptimes?v=v2\nhttp://ut.skywire.skycoin.com/uptimes?v=v2&visors=<pk1>;<pk2>;<pk3>",
	Run: func(cmd *cobra.Command, args []string) {
		log := logging.MustGetLogger("log-collecting")
		fver, err := version.NewVersion("v1.3.17")
		if err != nil {
			log.Fatal("can't parse version for filtering fetches")
		}
		if logOnly && surveyOnly {
			log.Fatal("use of mutually exclusive flags --log and --survey")
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()
		go func() {
			<-ctx.Done()
			cancel()
			os.Exit(1)
		}()

		// Preparing directories
		if _, err := os.ReadDir(writeDir); err != nil {
			if err := os.Mkdir(writeDir, 0750); err != nil {
				log.Error("Unable to create directory " + writeDir)
				return
			}
		}

		if err := os.Chdir(writeDir); err != nil {
			log.Error("Unable to change directory to " + writeDir)
			return
		}

		// Create dmsgcurl instance
		dg := dmsgcurl.New(flag.CommandLine)
		flag.Parse()

		// Set the uptime tracker to fetch data from
		endpoint := utAddr + "/uptimes?v=v2"

		if fetchFrom != "" {
			endpoint = utAddr + "&visors=" + fetchFrom
		}

		//Fetch the uptime data over http
		uptimes, err := getUptimes(endpoint, log)
		if err != nil {
			log.WithError(err).Error("Unable to get data from uptime tracker.")
			return
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
			log.Error(err)
			return
		}
		defer closeDmsg()

		// Connect dmsgC to all servers
		allServer := getAllDMSGServers()
		for _, server := range allServer {
			dmsgC.EnsureAndObtainSession(ctx, server.PK) //nolint
		}

		minimumVersion, _ := version.NewVersion(minv) //nolint
		incVerList := strings.Split(incVer, ",")

		start := time.Now()
		var bulkFolders []string
		// Get visors data
		var wg sync.WaitGroup
		for _, v := range uptimes {
			//only attempt to fetch from online visors
			if v.Online {
				if fetchFile == "" {
					visorVersion, err := version.NewVersion(v.Version) //nolint
					if v.Version == "" {
						log.Warnf("The version for visor %s is blank", v.PubKey)
						continue
					}
					includeV := contains(incVerList, v.Version)
					if err != nil && !includeV {
						log.Warnf("The version %s for visor %s is not valid", v.Version, v.PubKey)
						continue
					}
					if !allVisors && visorVersion.LessThan(minimumVersion) && !includeV {
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
								log.Errorf("Unable to create directory for visor %s", key)
								return
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
							if visorVersion.LessThan(fver) {
								download(ctx, log, httpC, "node-info.json", "node-info.json", key, maxFileSize) //nolint
							} else {
								download(ctx, log, httpC, "node-info", "node-info.json", key, maxFileSize) //nolint
							}
						}
						if !surveyOnly {
							for i := 0; i <= duration; i++ {
								date := time.Now().AddDate(0, 0, -i).UTC().Format("2006-01-02")
								download(ctx, log, httpC, date+".csv", date+".csv", key, maxFileSize) //nolint
							}
						}
					}(v.PubKey, &wg)
					batchSize--
					if batchSize == 0 {
						time.Sleep(15 * time.Second)
						batchSize = 50
					}
				}
				//omit the filters if a file was specified
				if fetchFile != "" {
					wg.Add(1)
					go func(key string, wg *sync.WaitGroup) {
						httpC := http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC), Timeout: 10 * time.Second}
						defer httpC.CloseIdleConnections()
						defer wg.Done()
						if _, err := os.ReadDir(key); err != nil {
							if err := os.Mkdir(key, 0750); err != nil {
								log.Errorf("Unable to create directory for visor %s", key)
								return
							}
						}
						_ = download(ctx, log, httpC, fetchFile, fetchFile, key, maxFileSize) //nolint
					}(v.PubKey, &wg)
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

	if err := downloadDmsg(ctx, log, &httpC, file, target, maxSize); err != nil {
		log.WithError(err).Errorf("The %s for visor %s not available", fileName, pubkey)
		return err
	}
	return nil
}

// downloadDmsg downloads a file from the given URL into 'w'.
func downloadDmsg(ctx context.Context, log logrus.FieldLogger, httpC *http.Client, w io.Writer, urlStr string, maxSize int64) error {
	req, err := http.NewRequest(http.MethodGet, urlStr, nil)
	if err != nil {
		log.WithError(err).Fatal("Failed to formulate HTTP request.")
	}
	resp, err := httpC.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to HTTP server: %w", err)
	}
	if resp.StatusCode == http.StatusOK {
		// 200 OK
		if maxSize > 0 {
			if resp.ContentLength > maxSize*1024 {
				return fmt.Errorf("requested file size is more than allowed size: %d KB > %d KB", (resp.ContentLength / 1024), maxSize)
			}
		}
		n, err := CancellableCopy(ctx, w, resp.Body, resp.ContentLength)
		if err != nil {
			return fmt.Errorf("download failed at %d/%dB: %w", n, resp.ContentLength, err)
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				log.WithError(err).Warn("HTTP Response body closed with non-nil error.")
			}
		}()
		return nil
	}
	// Convert the non-200 status code to an error
	return &httpError{Status: resp.StatusCode}
}

type readerFunc func(p []byte) (n int, err error)

func (rf readerFunc) Read(p []byte) (n int, err error) { return rf(p) }

// CancellableCopy will call the Reader and Writer interface multiple time, in order
// to copy by chunk (avoiding loading the whole file in memory).
func CancellableCopy(ctx context.Context, w io.Writer, body io.ReadCloser, length int64) (int64, error) {

	n, err := io.Copy(io.MultiWriter(w, &ProgressWriter{Total: length}), readerFunc(func(p []byte) (int, error) {

		// golang non-blocking channel: https://gobyexample.com/non-blocking-channel-operations
		select {

		// if context has been canceled
		case <-ctx.Done():
			// stop process and propagate "Download Canceled" error
			return 0, errors.New("Download Canceled")
		default:
			// otherwise just run default io.Reader implementation
			return body.Read(p)
		}
	}))
	return n, err
}

// ProgressWriter prints the progress of a download to stdout.
type ProgressWriter struct {
	// atomic requires 64-bit alignment for struct field access
	Current int64
	Total   int64
}

// Write implements io.Writer
func (pw *ProgressWriter) Write(p []byte) (int, error) {
	n := len(p)

	current := atomic.AddInt64(&pw.Current, int64(n))
	total := atomic.LoadInt64(&pw.Total)
	pc := fmt.Sprintf("%d%%", current*100/total)
	fmt.Printf("Downloading: %d/%dB (%s)", current, total, pc)
	if current != total {
		fmt.Print("\r")
	} else {
		fmt.Print("\n")
	}
	return n, nil
}

func getUptimes(endpoint string, log *logging.Logger) ([]VisorUptimeResponse, error) {
	var results []VisorUptimeResponse
	client := http.Client{
		Timeout: 60 * time.Second,
	}
	response, err := client.Get(endpoint) //nolint
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

func contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}
	return false
}

type httpError struct {
	Status int
}

func (e *httpError) Error() string {
	return fmt.Sprintf("http error: %d", e.Status)
}
