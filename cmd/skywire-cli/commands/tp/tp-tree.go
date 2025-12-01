// Package clitp cmd/skywire-cli/commands/tp/tp-tree.go
package clitp

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/bitfield/script"
	"github.com/pterm/pterm"
	"github.com/pterm/pterm/putils"
	"github.com/spf13/cobra"
	"github.com/tidwall/pretty"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
)

func init() {
	treeCmd.Flags().StringVarP(&rootNode, "source", "k", "", "root node ; defaults to visor with most transports")
	treeCmd.Flags().StringVarP(&lastNode, "dest", "d", "", "map route between source and dest")
	treeCmd.Flags().StringVarP(&tpdURL, "tpdurl", "a", deployment.Prod.TransportDiscovery, "transport discovery url")
	treeCmd.Flags().StringVarP(&utURL, "uturl", "w", deployment.Prod.UptimeTracker, "uptime tracker url")
	treeCmd.Flags().BoolVarP(&rawData, "raw", "r", false, "print raw json data")
	treeCmd.Flags().BoolVarP(&refinedData, "pretty", "p", false, "print pretty json data")
	treeCmd.Flags().BoolVarP(&noFilterOnline, "noton", "o", false, "do not filter by online status in UT")
	treeCmd.Flags().BoolVarP(&onlyOnline, "good", "g", false, "do not display transports for offline visors")
	treeCmd.Flags().StringVar(&cacheFileTPD, "cft", os.TempDir()+"/tpd.json", "TPD cache file location")
	treeCmd.Flags().StringVar(&cacheFileUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location.")
	treeCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes")
	//TODO: calculate tree levels initially and apply appropriate padding ; as an alternative to manually padding
	treeCmd.Flags().IntVarP(&padSpaces, "pad", "P", 15, "padding between tree and tpid")
	treeCmd.Flags().BoolVarP(&isStats, "stats", "s", false, "return only statistics")
}

var treeCmd = &cobra.Command{
	Use:   "tree",
	Short: "tree map of transports on the skywire network",
	Long:  fmt.Sprintf("display a tree representation of transports from TPD\n\n%v/all-transports\n\nSet cache file location to \"\" to avoid using cache files", deployment.Prod.TransportDiscovery),
	Run: func(cmd *cobra.Command, _ []string) {
		if rootNode != "" {
			err := rootnode.Set(rootNode)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), errors.New("invalid source or root node public key"))
			}
			if lastNode != "" {
				err := lastnode.Set(lastNode)
				if err != nil {
					internal.PrintFatalError(cmd.Flags(), errors.New("invalid dest or last node public key"))
				}
			}
		} else {
			if lastNode != "" {
				internal.PrintFatalError(cmd.Flags(), errors.New("-k, --source <public-key> is missing ; required with -d, --dest <public-key>"))
			}
		}
		tps := internal.GetData(cacheFileTPD, tpdURL+"/all-transports", cacheFilesAge)
		if rawData {
			script.Echo(tps).Stdout() //nolint:errcheck,gosec
			return
		}
		if refinedData {
			script.Echo(string(pretty.Color(pretty.Pretty([]byte(tps)), nil))).Stdout() //nolint:errcheck,gosec
			return
		}
		var uts string
		var utkeys []string
		var offlinekeys []string
		if !noFilterOnline {
			uts = internal.GetData(cacheFileUT, utURL+"/uptimes?v=v2", cacheFilesAge)
			utkeys, _ = script.Echo(uts).JQ(".[] | select(.on) | .pk").Replace("\"", "").Slice()             //nolint:errcheck
			offlinekeys, _ = script.Echo(uts).JQ(".[] | select(.on  | not) | .pk").Replace("\"", "").Slice() //nolint:errcheck
		}

		sortedEdgeKeys, _ = script.Echo(tps).JQ(".[].edges[]").Freq().Column(2).Slice() //nolint:errcheck

		if isStats {
			fmt.Printf("Unique keys in Transport Discovery: %d\n", len(sortedEdgeKeys))
			tpcount, _ := script.Echo(tps).JQ(".[].type").CountLines() //nolint:errcheck
			fmt.Printf("Count of transports: %v\n", tpcount)
			tptypes, _ := script.Echo(tps).JQ(".[].type").Freq().String() //nolint:errcheck
			fmt.Printf("types of transports: \n%v\n", tptypes)
			vcount, _ := script.Echo(tps).JQ(".[].edges[]").Freq().String() //nolint:errcheck
			fmt.Printf("Visors by transport count:\n%v\n", vcount)
			return
		}

		var usedkeys []string
		if onlyOnline {
			var onlineSortedEdgeKeys []string
			for i, v := range sortedEdgeKeys {
				found := false
				for _, k := range utkeys {
					if strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(k, " ", ""), "\t", ""), "\n", ""), "\"", "") == strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(v, " ", ""), "\t", ""), "\n", ""), "\"", "") {
						found = true
						break
					}
				}
				if found {
					onlineSortedEdgeKeys = append(onlineSortedEdgeKeys, sortedEdgeKeys[i])
				} else {
					usedkeys = append(usedkeys, sortedEdgeKeys[i])
				}
			}
			sortedEdgeKeys = onlineSortedEdgeKeys
		}

		fmt.Printf("Tree        *Online        %s        %s      %s      %s    TPID                                 Type\n", pterm.Black(pterm.BgRed.Sprint("*Offline")), pterm.Red("*Not in UT"), pterm.Blue(pterm.BgMagenta.Sprint("*source")), pterm.Magenta(pterm.BgBlue.Sprint("*dest")))
		leveledList := pterm.LeveledList{}
		if rootNode != "" {
			x := -1
			for i, v := range sortedEdgeKeys {
				if v == `"`+rootnode.String()+`"` {
					x = i
					break
				}
			}
			if x != -1 {
				for i := x; i > 0; i-- {
					sortedEdgeKeys[i] = sortedEdgeKeys[i-1]
				}
				sortedEdgeKeys[0] = `"` + rootnode.String() + `"`
			}
			if sortedEdgeKeys[0] != `"`+rootnode.String()+`"` {
				internal.PrintFatalError(cmd.Flags(), errors.New("specified source or root node public key does not have any transports"))
			}
		}
		if lastNode != "" {
			x := -1
			for i, v := range sortedEdgeKeys {
				if v == `"`+rootnode.String()+`"` {
					x = i
					break
				}
			}
			if x != -1 {
				for i := x; i > 1; i-- {
					sortedEdgeKeys[i] = sortedEdgeKeys[i-1]
				}
				sortedEdgeKeys[1] = `"` + lastnode.String() + `"`
			}
			if sortedEdgeKeys[1] != `"`+lastnode.String()+`"` {
				internal.PrintFatalError(cmd.Flags(), errors.New("specified dest or last node public key does not have any transports"))
			}
		}
		edgeKey := sortedEdgeKeys[0]
		leveledList = append(leveledList, pterm.LeveledListItem{Level: 0, Text: filterOnlineStatus(utkeys, offlinekeys, edgeKey)})

		usedkeys = append(usedkeys, edgeKey)
		var lvl func(n int, k string)
		lvl = func(n int, k string) {
			l, _ := script.Echo(tps).JQ(".[] | select(.edges[] == " + k + ") | .edges[] | select(. != " + k + ")").Slice() //nolint:errcheck
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
					tpid, _ = script.Echo(tps).JQ(".[] | select(.edges | index(" + k + ") and index(" + m + ")) | .t_id + \" \" + .type").First(1).String() //nolint:errcheck
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
		pterm.DefaultTree.WithRoot(putils.TreeFromLeveledList(leveledList)).Render() //nolint:errcheck,gosec

		for _, edgeKey := range sortedEdgeKeys {
			found := false
			for _, usedKey := range usedkeys {
				if usedKey == edgeKey {
					found = true
					break
				}
				if lastNode != "" && strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(usedKey, " ", ""), "\t", ""), "\n", ""), "\"", "") == strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(`"`+lastnode.String()+`"`, " ", ""), "\t", ""), "\n", ""), "\"", "") {
					found = true
					break
				}
			}
			if !found {
				leveledList = pterm.LeveledList{}
				leveledList = append(leveledList, pterm.LeveledListItem{Level: 0, Text: filterOnlineStatus(utkeys, offlinekeys, edgeKey)})
				usedkeys = append(usedkeys, edgeKey)
				lvl(1, edgeKey)
				if len(leveledList) > 1 {
					pterm.DefaultTree.WithRoot(putils.TreeFromLeveledList(leveledList)).Render() //nolint:errcheck,gosec
				}
				if lastNode != "" {
					pterm.Println(pterm.Red("No route from source to dest"))
					return
				}
			}
		}
		if lastNode != "" && rootNode != "" {
			x := -1
			for i, v := range usedkeys {
				if v == `"`+rootnode.String()+`"` {
					x = i
					break
				}
			}
			if x != -1 {
				for i := x; i > 0; i-- {
					usedkeys[i] = usedkeys[i-1]
				}
				usedkeys[0] = `"` + rootnode.String() + `"`
			}
			if usedkeys[0] != `"`+rootnode.String()+`"` {
				internal.PrintFatalError(cmd.Flags(), errors.New("specified source or root node public key does not have any transports"))
			}
			if lastNode != "" {
				x := -1
				for i, v := range usedkeys {
					if v == `"`+rootnode.String()+`"` {
						x = i
						break
					}
				}
				if x != -1 {
					for i := x; i > 1; i-- {
						usedkeys[i] = usedkeys[i-1]
					}
					usedkeys[1] = `"` + lastnode.String() + `"`
				}
				if usedkeys[1] != `"`+lastnode.String()+`"` {
					internal.PrintFatalError(cmd.Flags(), errors.New("specified dest or last node public key does not have any transports"))
				}
			}
			l, _ := script.Echo(tps).JQ("[.[] | select(.edges | contains([" + sortedEdgeKeys[0] + "," + sortedEdgeKeys[1] + "]))]").Slice() //nolint:errcheck
			if len(l) > 0 && fmt.Sprintf("%v", l) != "[[]]" {
				pterm.Println(pterm.Red("Direct route:"))
				for _, m := range l {
					script.Echo(string(pretty.Color(pretty.Pretty([]byte(m)), nil))).Stdout() //nolint:errcheck,gosec
				}
				return
			}
			var routeSlice []string
			var listLevel int
			re := regexp.MustCompile(`\s+`)
			for i := len(leveledList) - 1; i >= 0; i-- {
				if len(routeSlice) == 0 && strings.Contains(leveledList[i].Text, lastnode.String()) {
					rStepTpid, _ := script.Echo(fmt.Sprintf("%v", leveledList[i].Text)).ReplaceRegexp(re, " ").Column(2).Replace("\n", "").String()       //nolint:errcheck
					rStepTp, _ := script.Echo(tps).JQ(".[] | select(.t_id == "+`"`+strings.TrimRight(rStepTpid, "\n")+`"`+")").Replace("\n", "").String() //nolint:errcheck
					routeSlice = append(routeSlice, rStepTp)
					listLevel = leveledList[i].Level
				}
				if len(routeSlice) > 0 && leveledList[i].Level == (listLevel-1) {
					rStepTpid, _ := script.Echo(fmt.Sprintf("%v", leveledList[i].Text)).ReplaceRegexp(re, " ").Column(2).Replace("\n", "").String()       //nolint:errcheck
					rStepTp, _ := script.Echo(tps).JQ(".[] | select(.t_id == "+`"`+strings.TrimRight(rStepTpid, "\n")+`"`+")").Replace("\n", "").String() //nolint:errcheck
					routeSlice = append(routeSlice, rStepTp)
					listLevel = leveledList[i].Level
				}
			}
			pterm.Println(pterm.Red("Forward Route:"))
			for i := len(routeSlice) - 1; i >= 0; i-- {
				fmt.Printf("%v\n", routeSlice[i])
			}
			pterm.Println(pterm.Red("Reverse Route:"))
			for _, r := range routeSlice {
				fmt.Printf("%v\n", r)
			}
			/*
						var tM tpdMaps
						for i, v := range usedkeys {
							tM[i].PK = v
						}
						for i, v := range tM {
							l, _ := script.Echo(tps).JQ(".[] | select(.edges[] == " + v + ") | .edges[] | select(. != " + v + ")").Slice()
							for _, m := range l {
								if m == v {
									continue
								}
								tM[i].Edges = append(tM[i].Edges,v)
							}
						}
						for i, v := range tM {
							for j, w := range v.Edges {

					}
				}
			*/
		}
		if rootNode == "" && !onlyOnline {
			l, _ := script.Echo(tps).JQ(".[] | select(.edges[0] == .edges[1]) | .edges[0] + \""+strings.Repeat(" ", padSpaces)+"\" + .t_id + \" \" + .type").Replace("\"", "").Slice() //nolint:errcheck
			if len(l) > 0 {
				pterm.Println(pterm.Red("Self-transports"))
				for _, m := range l {
					pterm.Println(filterOnlineStatus(utkeys, offlinekeys, m))
				}
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
		lvlN = pterm.Red(lvlN)
	}
	if isOffline {
		lvlN = pterm.Black(pterm.BgRed.Sprint(lvlN))
	}
	if lastNode != "" && strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(`"`+lastnode.String()+`"`, " ", ""), "\t", ""), "\n", ""), "\"", "") == strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(key, " ", ""), "\t", ""), "\n", ""), "\"", "") {
		lvlN = pterm.Magenta(pterm.BgBlue.Sprint(lvlN))
	}
	if rootNode != "" && strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(`"`+rootnode.String()+`"`, " ", ""), "\t", ""), "\n", ""), "\"", "") == strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(key, " ", ""), "\t", ""), "\n", ""), "\"", "") {
		lvlN = pterm.Blue(pterm.BgMagenta.Sprint(lvlN))
	}
	return lvlN
}
