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
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"

	"github.com/spf13/cobra"
)

var pubkey cipher.PubKey
var pk string
var thisPk string

var minUT int
func init() {
	RootCmd.Flags().StringVarP(&pk, "pk", "k", "", "check uptime for the specified key")
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

		// startDate := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
		// endDate := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Add(-1 * time.Second).Format("2006-01-02")
		startDate := time.Date(now.Year(), now.Month(), -1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
		endDate := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location()).Add(-1 * time.Second).Format("2006-01-02")
		uptimes := Uptimes{}
		jsonErr := json.Unmarshal(body, &uptimes)
		if jsonErr != nil {
			log.Fatal(jsonErr)
		}
		for _, j := range uptimes {
			thisPk = j.Pk
			selectedDaily(j.Daily, startDate, endDate)
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
