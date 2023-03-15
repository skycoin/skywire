// Package cliut root.go

package cliut

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"time"
	"os"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"

	"github.com/spf13/cobra"
)

var pubkey cipher.PubKey
var pk string
var thisPk string
var online bool
var isStats      bool


var minUT int
func init() {
	RootCmd.Flags().StringVarP(&pk, "pk", "k", "", "check uptime for the specified key")
	RootCmd.Flags().BoolVarP(&online, "on", "o", false, "list currently online visors")
	RootCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "count the number of results")
	RootCmd.Flags().IntVarP(&minUT, "min", "n", 75, "list visors meeting minimum uptime")
}


// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "ut",
	Short: "query uptime tracker",
	Run: func(cmd *cobra.Command, _ []string) {
		url := "http://ut.skywire.skycoin.com/uptimes?v=v2"
		now := time.Now()
		if pk != "" {
			err := pubkey.Set(pk)
			if err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Invalid or missing public key"))
			} else {
				url = "http://ut.skywire.skycoin.com/uptimes?v=v2&visors="+pubkey.String()
		}
	}
		utClient := http.Client{
			Timeout: time.Second * 15, // Timeout after 2 seconds
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
			defer res.Body.Close()
		}

		body, readErr := ioutil.ReadAll(res.Body)
		if readErr != nil {
			log.Fatal(readErr)
		}

		startDate := time.Date(now.Year(), now.Month(), -1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
		endDate := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location()).Add(-1 * time.Second).Format("2006-01-02")
		uptimes := Uptimes{}
		jsonErr := json.Unmarshal(body, &uptimes)
		if jsonErr != nil {
			log.Fatal(jsonErr)
		}
		var msg []string
		for _, j := range uptimes {
			thisPk = j.Pk
			if online {
				if j.On {
					msg = append(msg, fmt.Sprintf(thisPk+"\n"))
				}
			} else {
				selectedDaily(j.Daily, startDate, endDate)
			}
		}
		if online {
			if isStats {
				internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%d visors online\n", len(msg)), fmt.Sprintf("%d visors online\n", len(msg)))
				os.Exit(0)
			}
			for _, i := range msg {
				internal.PrintOutput(cmd.Flags(), fmt.Sprintf("%s", i), fmt.Sprintf("%s", i))
		}
		}
	},
}

func selectedDaily(data map[string]string, startDate, endDate string) {
	for date, uptime := range data {
		if date >= startDate && date <= endDate {
			utfloat, err := strconv.ParseFloat(uptime, 64)
			if err != nil {
				log.Fatal(err)
			}
			if utfloat >= float64(minUT) {
				//        if date == startDate {
				fmt.Printf(thisPk)
				fmt.Printf(" ")
				fmt.Println(date, uptime)
				//        }
			}
		}
	}
}


type Uptimes []struct {
	Pk    string            `json:"pk"`
	Up    int               `json:"up"`
	Down  int               `json:"down"`
	Pct   float64           `json:"pct"`
	On    bool              `json:"on"`
	Daily map[string]string `json:"daily,omitempty"`
}
