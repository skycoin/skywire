// Package cliut cmd/skywire-cli/ut/root.go
package cliut

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/pkg/direct"
	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
)

var (
	pubkey     cipher.PubKey
	pk         string
	thisPk     string
	online     bool
	isStats    bool
	url        string
	data       []byte
	dmsgAddr   string
	dmsgIP     string
	utDmsgAddr string
)

var minUT int

func init() {
	RootCmd.Flags().StringVarP(&pk, "pk", "k", "", "check uptime for the specified key")
	RootCmd.Flags().BoolVarP(&online, "on", "o", false, "list currently online visors")
	RootCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "count the number of results")
	RootCmd.Flags().IntVarP(&minUT, "min", "n", 75, "list visors meeting minimum uptime")
	RootCmd.Flags().StringVarP(&url, "url", "u", "http://ut.skywire.skycoin.com/uptimes?v=v2", "specify alternative uptime tracker url\ndefault: http://ut.skywire.skycoin.com/uptimes?v=v2")
	RootCmd.Flags().StringVar(&dmsgAddr, "dmsgAddr", "030c83534af1041aee60c2f124b682a9d60c6421876db7c67fc83a73c5effdbd96", "specific dmsg server address for dmsghttp query")
	RootCmd.Flags().StringVar(&dmsgIP, "dmsgIP", "188.121.99.59:8081", "specific dmsg server ip for dmsghttp query")
	RootCmd.Flags().StringVar(&utDmsgAddr, "utDmsgAddr", "dmsg://022c424caa6239ba7d1d9d8f7dab56cd5ec6ae2ea9ad97bb94ad4b48f62a540d3f:80", "dmsg address of uptime tracker")
}

// RootCmd contains commands that interact with the skywire-visor
var RootCmd = &cobra.Command{
	Use:   "ut",
	Short: "query uptime tracker",
	Long:  "query uptime tracker\n Check local visor daily uptime percent with:\n skywire-cli ut -k $(skywire-cli visor pk)",
	Run: func(cmd *cobra.Command, _ []string) {
		// tyring to connect with running visor
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		} else {
			data, err = rpcClient.FetchUptimeTrackerData(pk)
			if err != nil {
				internal.PrintError(cmd.Flags(), fmt.Errorf("unable to fetch uptime tracker data from RPC client: %w", err))
			}
		}
		// no rpc, so trying to dmsghttp query
		if len(data) == 0 {
			data, err = dmsgHTTPQuery(cmd)
			if err != nil {
				internal.PrintError(cmd.Flags(), fmt.Errorf("unable to fetch uptime tracker data in dmsgHTTPQuery method: %w", err))
			}
		}
		// nor rpc and dmsghttp, trying to direct http query
		if len(data) == 0 {
			data, err = httpQuery(cmd)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to fetch uptime tracker data in httpQuery method: %w", err))
			}
		}
		now := time.Now()
		startDate := time.Date(now.Year(), now.Month(), -1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
		endDate := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location()).Add(-1 * time.Second).Format("2006-01-02")
		uts := uptimes{}
		jsonErr := json.Unmarshal(data, &uts)
		if jsonErr != nil {
			log.Fatal(jsonErr)
		}
		var msg []string
		for _, j := range uts {
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
				internal.PrintOutput(cmd.Flags(), i, i)
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
				fmt.Print(thisPk)
				fmt.Print(" ")
				fmt.Println(date, uptime)
			}
		}
	}
}

func dmsgHTTPQuery(cmd *cobra.Command) ([]byte, error) {
	pk, sk := cipher.GenerateKeyPair()
	var dmsgAddrPK cipher.PubKey
	err := dmsgAddrPK.Set(dmsgAddr)
	if err != nil {
		return []byte{}, err
	}

	servers := []*disc.Entry{{Server: &disc.Server{Address: dmsgIP}, Static: dmsgAddrPK}}
	keys := cipher.PubKeys{pk}

	entries := direct.GetAllEntries(keys, servers)
	dClient := direct.NewClient(entries, logging.NewMasterLogger().PackageLogger("ut_dmsgHTTPQuery"))

	dmsgDC, closeDmsgDC, err := direct.StartDmsg(cmd.Context(), logging.NewMasterLogger().PackageLogger("ut_dmsgHTTPQuery"),
		pk, sk, dClient, dmsg.DefaultConfig())
	if err != nil {
		return []byte{}, fmt.Errorf("failed to start dmsg: %w", err)
	}
	defer closeDmsgDC()

	dmsgHTTP := http.Client{Transport: dmsghttp.MakeHTTPTransport(cmd.Context(), dmsgDC)}

	resp, err := dmsgHTTP.Get(utDmsgAddr + "/uptimes?v=v2")
	if err != nil {
		return []byte{}, err
	}
	return io.ReadAll(resp.Body)
}

func httpQuery(cmd *cobra.Command) ([]byte, error) {
	if pk != "" {
		err := pubkey.Set(pk)
		if err != nil {
			return []byte{}, err
		}
		url += "&visors=" + pubkey.String()
	}
	utClient := http.Client{
		Timeout: time.Second * 15, // Timeout after 15 seconds
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return []byte{}, err
	}

	res, err := utClient.Do(req)
	if err != nil {
		return []byte{}, err
	}

	if res.Body != nil {
		defer func() {
			err := res.Body.Close()
			if err != nil {
				internal.PrintError(cmd.Flags(), fmt.Errorf("Failed to close response body"))
			}
		}()
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return []byte{}, err
	}
	return body, nil
}

type uptimes []struct {
	Pk    string            `json:"pk"`
	Up    int               `json:"up"`
	Down  int               `json:"down"`
	Pct   float64           `json:"pct"`
	On    bool              `json:"on"`
	Daily map[string]string `json:"daily,omitempty"`
}
