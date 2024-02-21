// Package clirtfind subcommand for skywire-cli
package clirtree

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/pterm/pterm"
	"github.com/pterm/pterm/putils"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
)

var (
	sortedEdgeKeys []string
	utURL          string
	tpdURL         string
	cacheFileTPD   string
	cacheFileUT    string
	cacheFilesAge  int
	isStats        bool
	rawData        bool
	refinedData    bool
	RootCmd        = rtreeCmd
)

func init() {
	rtreeCmd.Flags().StringVarP(&tpdURL, "tpdurl", "a", utilenv.TpDiscAddr, "transport discovery url")
	rtreeCmd.Flags().StringVarP(&utURL, "uturl", "w", utilenv.UptimeTrackerAddr, "uptime tracker url")
	rtreeCmd.Flags().BoolVarP(&rawData, "raw", "r", false, "print raw json data")
	rtreeCmd.Flags().BoolVarP(&refinedData, "pretty", "p", false, "print pretty json data")
	//	rtreeCmd.Flags().BoolVarP(&noFilterOnline, "noton", "o", false, "do not filter by online status in UT")
	rtreeCmd.Flags().StringVar(&cacheFileTPD, "cfs", os.TempDir()+"/tpd.json", "TPD cache file location")
	rtreeCmd.Flags().StringVar(&cacheFileUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location.")
	rtreeCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes")
	rtreeCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "return only statistics")
}

var rtreeCmd = &cobra.Command{
	Use:   "rtree",
	Short: "map of transports on the skywire network",
	Long:  fmt.Sprintf("display a tree repersentation of transports on the skywire network from the transport discovery\n%v/api/services?type=%v\n\nSet cache file location to \"\" to avoid using cache files", utilenv.TpDiscAddr),
	Run: func(cmd *cobra.Command, args []string) {
		tps := getData(cacheFileTPD, tpdURL+"/all-transports")
		if rawData {
			script.Echo(tps).Stdout() //nolint
			return
		}
		if refinedData {
			script.Echo(string(pretty.Color(pretty.Pretty([]byte(tps)), nil))).Stdout() //nolint
			return
		}
		//		if isStats {
		//		}

		sortedEdgeKeys, _ = script.Echo(tps).JQ(".[].edges[]").Freq().Column(2).Slice()

		fmt.Println("Tree" + strings.Repeat(" ", 82-5) + "TPID" + strings.Repeat(" ", 37-4) + "Type")

		leveledList := pterm.LeveledList{}
		edgeKey := sortedEdgeKeys[0]
		leveledList = append(leveledList, pterm.LeveledListItem{Level: 0, Text: strings.ReplaceAll(edgeKey, "\"", "")})

		var usedkeys []string
		usedkeys = append(usedkeys, edgeKey)
		var lvl func(n int, k string)
		lvl = func(n int, k string) {
			l, _ := script.Echo(tps).JQ(".[] | select(.edges[] == " + k + ") | .edges[] | select(. != " + k + ")").Slice()
			for _, m := range l {
				if m == k {
					continue
				}
				var ok bool
				ok = false
				for _, o := range usedkeys {
					if m == o {
						ok = true
					}
				}
				if ok {
					continue
				}
				usedkeys = append(usedkeys, m)
				var tpid string
				if n == 0 {
					tpid = ""
				} else {
					tpid, _ = script.Echo(tps).JQ(".[] | select(.edges | index(" + k + ") and index(" + m + ")) | .t_id + \" \" + .type").First(1).String()
				}

				leveledList = append(leveledList, pterm.LeveledListItem{Level: n, Text: strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%s %s", m, strings.Repeat(" ", func() int {
					indent := 11 - n*2
					if indent < 0 {
						return 0
					}
					return indent
				}())+tpid), "\n", ""), "\"", "")})
				lvl(n+1, m)
			}
			return
		}
		lvl(1, edgeKey)

		pterm.DefaultTree.WithRoot(putils.TreeFromLeveledList(leveledList)).Render()
		for _, edgeKey := range sortedEdgeKeys {
			found := false
			for _, usedKey := range usedkeys {
				if usedKey == edgeKey {
					found = true
					break
				}
			}
			if !found {
				leveledList = pterm.LeveledList{}
				leveledList = append(leveledList, pterm.LeveledListItem{Level: 0, Text: strings.ReplaceAll(edgeKey, "\"", "")})
				usedkeys = append(usedkeys, edgeKey)
				lvl(1, edgeKey)
				pterm.DefaultTree.WithRoot(putils.TreeFromLeveledList(leveledList)).Render()
			}
		}
		l, _ := script.Echo(tps).JQ(".[] | select(.edges[0] == .edges[1]) | .edges[0] + \"               \" + .t_id + \" \" + .type").Replace("\"", "").Slice()
		if len(l) > 0 {
			pterm.Println(pterm.Red("Illegal self-transports"))
			for _, m := range l {
				script.Echo(m + "\n").Stdout()
			}
		}
	},
}

func stats() {
	//fetch data, write raw data to file, filter to string array sortedEdgeKeys
	fmt.Printf("Unique keys in Transport Discovery: %d\n", len(sortedEdgeKeys))
	tpcount, _ := script.File("./transports.json").JQ(".[].type").CountLines()
	fmt.Printf("Count of transports: %v\n", tpcount)
	tptypes, _ := script.File("./transports.json").JQ(".[].type").Freq().String()
	fmt.Printf("types of transports: \n%v\n", tptypes)
	vcount, _ := script.File("./transports.json").JQ(".[].edges[]").Freq().String()
	//notps, _ := script.Exec(`bash -c 'source rtree.sh ; _notps'`).String()
	fmt.Printf("Visors by transport count:\n%v\n", vcount)
	//fmt.Printf("Visors by transport count:\n%v\nVisors with no transports:\n%v\n", vcount, notps)
}

/*
		uts := getData(cacheFileUT, utURL+"/uptimes?v=v2")
		utkeys, _ := script.Echo(uts).JQ(".[] | select(.on) | .pk").Replace("\"", "").String() //nolint
		if isStats {
			count, _ := script.Echo(sdkeys + utkeys).Freq().Match("2 ").Column(2).CountLines() //nolint
			script.Echo(fmt.Sprintf("%v\n", count)).Stdout()                                   //nolint
			return
		}
		if !isLabel {
			script.Echo(sdkeys + utkeys).Freq().Match("2 ").Column(2).Stdout() //nolint
		} else {
			filteredKeys, _ := script.Echo(sdkeys + utkeys).Freq().Match("2 ").Column(2).Slice()                           //nolint
			formattedoutput, _ := script.Echo(sds).JQ(".[] | \"\\(.address) \\(.geo.country)\"").Replace("\"", "").Slice() //nolint
			// Very slow!
			for _, fo := range formattedoutput {
				for _, fk := range filteredKeys {
					script.Echo(fo).Match(fk).Stdout() //nolint
				}
			}
		}
	},
}
*/

func getData(cachefile, thisurl string) (thisdata string) {
	var shouldfetch bool
	buf1 := new(bytes.Buffer)
	cTime := time.Now()
	if cachefile == "" {
		thisdata, _ = script.NewPipe().WithHTTPClient(&http.Client{Timeout: 30 * time.Second}).Get(thisurl).String() //nolint
		return thisdata
	}
	if cachefile != "" {
		if u, err := os.Stat(cachefile); err != nil {
			shouldfetch = true
		} else {
			if cTime.Sub(u.ModTime()).Minutes() > float64(cacheFilesAge) {
				shouldfetch = true
			}
		}
		if shouldfetch {
			_, _ = script.NewPipe().WithHTTPClient(&http.Client{Timeout: 30 * time.Second}).Get(thisurl).Tee(buf1).WriteFile(cachefile) //nolint
			thisdata = buf1.String()
		} else {
			thisdata, _ = script.File(cachefile).String() //nolint
		}
	} else {
		thisdata, _ = script.NewPipe().WithHTTPClient(&http.Client{Timeout: 30 * time.Second}).Get(thisurl).String() //nolint
	}
	return thisdata
}
