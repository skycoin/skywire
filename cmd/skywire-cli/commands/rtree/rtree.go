// Package clirtree subcommand for skywire-cli
package clirtree

import (
	"fmt"
	"os"
	"strings"

	"github.com/bitfield/script"
	"github.com/pterm/pterm"
	"github.com/pterm/pterm/putils"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
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
)

// RootCmd is rtreeCmd
var RootCmd = rtreeCmd

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
	Long:  fmt.Sprintf("display a tree representation of transports from TPD\n\n%v/all-transports\n\nSet cache file location to \"\" to avoid using cache files", utilenv.TpDiscAddr),
	Run: func(cmd *cobra.Command, args []string) {
		tps := internal.GetData(cacheFileTPD, tpdURL+"/all-transports", cacheFilesAge)
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
			uts = internal.GetData(cacheFileUT, utURL+"/uptimes?v=v2", cacheFilesAge)
			utkeys, _ = script.Echo(uts).JQ(".[] | select(.on) | .pk").Replace("\"", "").Slice()             //nolint
			offlinekeys, _ = script.Echo(uts).JQ(".[] | select(.on  | not) | .pk").Replace("\"", "").Slice() //nolint
		}

		sortedEdgeKeys, _ = script.Echo(tps).JQ(".[].edges[]").Freq().Column(2).Slice() //nolint

		if isStats {
			fmt.Printf("Unique keys in Transport Discovery: %d\n", len(sortedEdgeKeys))
			tpcount, _ := script.Echo(tps).JQ(".[].type").CountLines() //nolint
			fmt.Printf("Count of transports: %v\n", tpcount)
			tptypes, _ := script.Echo(tps).JQ(".[].type").Freq().String() //nolint
			fmt.Printf("types of transports: \n%v\n", tptypes)
			vcount, _ := script.Echo(tps).JQ(".[].edges[]").Freq().String() //nolint
			fmt.Printf("Visors by transport count:\n%v\n", vcount)
			return
		}

		fmt.Printf("Tree        *Online        %s        %s                            TPID                                 Type\n", pterm.Black(pterm.BgRed.Sprint("*Offline")), pterm.Red("*Not in UT"))

		leveledList := pterm.LeveledList{}
		edgeKey := sortedEdgeKeys[0]
		leveledList = append(leveledList, pterm.LeveledListItem{Level: 0, Text: filterOnlineStatus(utkeys, offlinekeys, edgeKey)})

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
				leveledList = append(leveledList, pterm.LeveledListItem{Level: n, Text: strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%s %s", filterOnlineStatus(utkeys, offlinekeys, m), strings.Repeat(" ", func() int {
					indent := padSpaces - 4 - n*2
					if indent < 0 {
						return 0
					}
					return indent
				}())+tpid), "\n", ""), "\"", "")})
				lvl(n+1, m)
			}
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
				leveledList = append(leveledList, pterm.LeveledListItem{Level: 0, Text: filterOnlineStatus(utkeys, offlinekeys, edgeKey)})
				usedkeys = append(usedkeys, edgeKey)
				lvl(1, edgeKey)
				pterm.DefaultTree.WithRoot(putils.TreeFromLeveledList(leveledList)).Render() //nolint
			}
		}
		l, _ := script.Echo(tps).JQ(".[] | select(.edges[0] == .edges[1]) | .edges[0] + \""+strings.Repeat(" ", padSpaces)+"\" + .t_id + \" \" + .type").Replace("\"", "").Slice() //nolint
		if len(l) > 0 {
			pterm.Println(pterm.Red("Self-transports"))
			for _, m := range l {
				pterm.Println(filterOnlineStatus(utkeys, offlinekeys, m))
			}
		}
	},
}

func filterOnlineStatus(utkeys, offlinekeys []string, key string) (lvlN string) {
	isOnline, isOffline := false, false
	lvlN = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(key, " ", ""), "\t", ""), "\n", ""), "\"", "")
	if !noFilterOnline {
		for _, k := range utkeys {
			if strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(k, " ", ""), "\t", ""), "\n", ""), "\"", "") == strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(key, " ", ""), "\t", ""), "\n", ""), "\"", "") {
				isOnline = true
				break
			}
		}
		if !isOnline {
			for _, k := range offlinekeys {
				if strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(k, " ", ""), "\t", ""), "\n", ""), "\"", "") == strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(key, " ", ""), "\t", ""), "\n", ""), "\"", "") {
					isOffline = true
					break
				}
			}
		}
	} else {
		isOnline, isOffline = true, false
	}
	if !isOnline && !isOffline {
		lvlN = pterm.Red(strings.ReplaceAll(key, "\"", ""))
	}
	if isOffline {
		lvlN = pterm.Black(pterm.BgRed.Sprint(strings.ReplaceAll(key, "\"", "")))
	}
	return lvlN
}
