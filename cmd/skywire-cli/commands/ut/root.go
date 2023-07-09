// Package cliut cmd/skywire-cli/ut/root.go
package cliut

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
//	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

var (
	pubkey  cipher.PubKey
	pk      string
	thisPk  string
	online  bool
	isStats bool
	url     string
	ver     string
)

var minUT int

func init() {
	RootCmd.Flags().StringVarP(&pk, "pk", "k", "", "check uptime for the specified key")
	RootCmd.Flags().StringVarP(&ver, "ver", "v", "", "filter results by version")
	RootCmd.Flags().BoolVarP(&online, "on", "o", false, "list currently online visors")
	RootCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "count the number of results")
	RootCmd.Flags().IntVarP(&minUT, "min", "n", 75, "list visors meeting minimum uptime")
	RootCmd.Flags().StringVarP(&url, "url", "u", "https://ut.skywire.skycoin.com/uptimes?v=v2", "specify alternative uptime tracker URL")
}

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "ut",
	Short: "query uptime tracker",
	Long:  "query uptime tracker\n Check local visor daily uptime percent with:\n skywire-cli ut -k $(skywire-cli visor pk)",
	Run: func(cmd *cobra.Command, _ []string) {
		if pk != "" {
			err := pubkey.Set(pk)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Invalid or missing public key"))
			}
		}

		utClient := http.Client{
			Timeout: time.Second * 15, // Timeout after 15 seconds
		}

		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			log.Fatal(err)
		}

		res, getErr := utClient.Do(req)
		if getErr != nil {
			log.Fatal(getErr)
		}

		if res.Body != nil {
			defer func() {
				err := res.Body.Close()
				if err != nil {
					internal.PrintError(cmd.Flags(), fmt.Errorf("Failed to close response body"))
				}
			}()
		}

		body, readErr := io.ReadAll(res.Body)
		if readErr != nil {
			log.Fatal(readErr)
		}

		uts := uptimes{}
		jsonErr := json.Unmarshal(body, &uts)
		if jsonErr != nil {
			log.Fatal(jsonErr)
		}

		if online {
			msg := getOnlineVisors(uts)
			if isStats {
				internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%d visors online\n", len(msg)), fmt.Sprintf("%d visors online\n", len(msg)))
				os.Exit(0)
			}
			for _, i := range msg {
				internal.PrintOutput(cmd.Flags(), i, i)
			}
		} else {
			count := getFilteredResultsCount(uts, ver)
			if isStats {
				internal.PrintOutput(cmd.Flags(), fmt.Sprintf("Number of results with version %s: %d\n", ver, count), fmt.Sprintf("Number of results with version %s: %d\n", ver, count))
				os.Exit(0)
			}
			printFilteredResults(uts, ver)
		}
	},
}

func getOnlineVisors(uts uptimes) []string {
	var msg []string
	for _, j := range uts {
		if j.On {
			msg = append(msg, fmt.Sprintf(j.Pk+"\n"))
		}
	}
	return msg
}

func getFilteredResultsCount(uts uptimes, versionFilter string) int {
	count := 0
	for _, j := range uts {
		if versionFilter != "" && j.Version == versionFilter {
			count++
		}
	}
	return count
}

func printFilteredResults(uts uptimes, versionFilter string) {
	for _, j := range uts {
		if versionFilter != "" && j.Version == versionFilter {
			for date, uptime := range j.Daily {
				printResult(j.Pk, date, uptime)
			}
		} else if versionFilter == "" {
			for date, uptime := range j.Daily {
				printResult(j.Pk, date, uptime)
			}
		}
	}
}

func printResult(pk, date, uptime string) {
	fmt.Printf("%s %s %s\n", pk, date, uptime)
}

type uptimes []struct {
	Pk      string            `json:"pk"`
	On      bool              `json:"on"`
	Version string            `json:"version"`
	Daily   map[string]string `json:"daily,omitempty"`
}
