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

	"github.com/spf13/cobra"
)

var thisPk string

const minUT float64 = 75.00

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "ut",
	Short: "query uptime tracker",
	Run: func(cmd *cobra.Command, _ []string) {

		now := time.Now()
		url := "http://ut.skywire.skycoin.com/uptimes?v=v2"

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
			if utfloat >= minUT {
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
