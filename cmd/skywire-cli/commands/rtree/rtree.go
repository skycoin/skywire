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
	padSpaces      int
	isStats        bool
	rawData        bool
	refinedData    bool
	noFilterOnline bool
	RootCmd        = rtreeCmd
)

func init() {
	rtreeCmd.Flags().StringVarP(&tpdURL, "tpdurl", "a", utilenv.TpDiscAddr, "transport discovery url")
	rtreeCmd.Flags().StringVarP(&utURL, "uturl", "w", utilenv.UptimeTrackerAddr, "uptime tracker url")
	rtreeCmd.Flags().BoolVarP(&rawData, "raw", "r", false, "print raw json data")
	rtreeCmd.Flags().BoolVarP(&refinedData, "pretty", "p", false, "print pretty json data")
	rtreeCmd.Flags().BoolVarP(&noFilterOnline, "noton", "o", false, "do not filter by online status in UT")
	rtreeCmd.Flags().StringVar(&cacheFileTPD, "cft", os.TempDir()+"/tpd.json", "TPD cache file location")
	rtreeCmd.Flags().StringVar(&cacheFileUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location.")
	rtreeCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes")
	//TODO: calculate tree levels initially and apply appropriate padding ; as an alternative to manually padding
	rtreeCmd.Flags().IntVarP(&padSpaces, "pad", "P", 15, "padding between tree and tpid")
	rtreeCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "return only statistics")
}

var rtreeCmd = &cobra.Command{
	Use:   "rtree",
	Short: "map of transports on the skywire network",
	Long:  fmt.Sprintf("display a tree representation of transports from TPD\n\n%v/all-transports\n\nSet cache file location to \"\" to avoid using cache files\n\n*Online\n%s\n%s", utilenv.TpDiscAddr, pterm.BgRed.Sprint("*Offline"),pterm.Red("*Not in UT")),
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
		var uts string
		var utkeys []string
		var offlinekeys []string
		if !noFilterOnline {
			uts = getData(cacheFileUT, utURL+"/uptimes?v=v2")
			utkeys, _ = script.Echo(uts).JQ(".[] | select(.on) | .pk").Replace("\"", "").Slice() //nolint
			offlinekeys, _ = script.Echo(uts).JQ(".[] | select(.on  | not) | .pk").Replace("\"", "").Slice() //nolint
		}

		sortedEdgeKeys, _ = script.Echo(tps).JQ(".[].edges[]").Freq().Column(2).Slice() //nolint

		if isStats {
			fmt.Printf("Unique keys in Transport Discovery: %d\n", len(sortedEdgeKeys))
			tpcount, _ := script.Echo(tps).JQ(".[].type").CountLines()
			fmt.Printf("Count of transports: %v\n", tpcount)
			tptypes, _ := script.Echo(tps).JQ(".[].type").Freq().String()
			fmt.Printf("types of transports: \n%v\n", tptypes)
			vcount, _ := script.Echo(tps).JQ(".[].edges[]").Freq().String()
			fmt.Printf("Visors by transport count:\n%v\n", vcount)
			return
		}

		fmt.Println("Tree" + strings.Repeat(" ", 67+padSpaces-5) + "TPID" + strings.Repeat(" ", 37-4) + "Type")

		leveledList := pterm.LeveledList{}
		edgeKey := sortedEdgeKeys[0]
		isOnline := false
		isOffline := false
		lvlZero := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(edgeKey, " ", ""), "\t", ""), "\n", ""), "\"", "")
		if !noFilterOnline {
			for _, key := range utkeys {
				if strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(key, " ", ""), "\t", ""), "\n", ""), "\"", "") == lvlZero {
					isOnline = true
					break
				}
			}
			for _, key := range offlinekeys {
				if strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(key, " ", ""), "\t", ""), "\n", ""), "\"", "") == lvlZero {
					isOffline = true
					break
				}
			}
		} else {
			isOnline = true
		}

		if !isOnline && !isOffline {
			lvlZero = pterm.Red(strings.ReplaceAll(edgeKey, "\"", ""))
		}
		if isOffline {
			lvlZero = pterm.BgRed.Sprint(strings.ReplaceAll(edgeKey, "\"", ""))
		}
		leveledList = append(leveledList, pterm.LeveledListItem{Level: 0, Text: lvlZero})

		var usedkeys []string
		usedkeys = append(usedkeys, edgeKey)
		var lvl func(n int, k string)
		lvl = func(n int, k string) {
			l, _ := script.Echo(tps).JQ(".[] | select(.edges[] == " + k + ") | .edges[] | select(. != " + k + ")").Slice() //nolint
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
					tpid, _ = script.Echo(tps).JQ(".[] | select(.edges | index(" + k + ") and index(" + m + ")) | .t_id + \" \" + .type").First(1).String() //nolint
				}
				isOnline := false
				isOffline := false

				lvlN := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(m, " ", ""), "\t", ""), "\n", ""), "\"", "")
				if !noFilterOnline {
					for _, key := range utkeys {
						if strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(key, " ", ""), "\t", ""), "\n", ""), "\"", "") == lvlN {
							isOnline = true
							break
						}
					}
					for _, key := range offlinekeys {
						if strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(key, " ", ""), "\t", ""), "\n", ""), "\"", "") == lvlZero {
							isOffline = true
							break
						}
					}
				} else {
					isOnline = true
				}
				if !isOnline && !isOffline {
					lvlN = pterm.Red(strings.ReplaceAll(m, "\"", ""))
				}
				if isOffline {
					lvlN = pterm.BgRed.Sprint(strings.ReplaceAll(m, "\"", ""))
				}
				leveledList = append(leveledList, pterm.LeveledListItem{Level: n, Text: strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%s %s", lvlN, strings.Repeat(" ", func() int {
					indent := padSpaces - 4 - n*2
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

		pterm.DefaultTree.WithRoot(putils.TreeFromLeveledList(leveledList)).Render() //nolint
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
				isOnline := false
				isOffline := false
				lvlZero := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(edgeKey, " ", ""), "\t", ""), "\n", ""), "\"", "")
				if !noFilterOnline {
					for _, key := range utkeys {
						if strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(key, " ", ""), "\t", ""), "\n", ""), "\"", "") == lvlZero {
							isOnline = true
							break
						}
					}
					for _, key := range offlinekeys {
						if strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(key, " ", ""), "\t", ""), "\n", ""), "\"", "") == lvlZero {
							isOffline = true
							break
						}
					}
				} else {
					isOnline = true
				}
				if !isOnline && !isOffline {
					lvlZero = pterm.Red(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(edgeKey, " ", ""), "\t", ""), "\n", ""), "\"", ""))
				}
				if isOffline {
					lvlZero = pterm.BgRed.Sprint(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(edgeKey, " ", ""), "\t", ""), "\n", ""), "\"", ""))
				}
				leveledList = append(leveledList, pterm.LeveledListItem{Level: 0, Text: lvlZero})
				usedkeys = append(usedkeys, edgeKey)
				lvl(1, edgeKey)
				pterm.DefaultTree.WithRoot(putils.TreeFromLeveledList(leveledList)).Render() //nolint
			}
		}
		l, _ := script.Echo(tps).JQ(".[] | select(.edges[0] == .edges[1]) | .edges[0] + \""+strings.Repeat(" ", padSpaces)+"\" + .t_id + \" \" + .type").Replace("\"", "").Slice()
		if len(l) > 0 {
			pterm.Println(pterm.Red("Self-transports"))
			for _, m := range l {
				isOnline := false
				lvlZero = m
				if !noFilterOnline {
					for _, key := range utkeys {
						if strings.Contains(m, key) {
							isOnline = true
							break
						}
					}
				} else {
					isOnline = true
				}
				if !isOnline {
					lvlZero = pterm.Red(m)
				}
				pterm.Println(lvlZero)

				script.Echo(m + "\n").Stdout()
			}
		}
	},
}

/*
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
