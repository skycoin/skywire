// Package clitp cmd/skywire-cli/commands/tp/tp.go
package clitp

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bitfield/script"
	"github.com/google/uuid"
	"github.com/pterm/pterm"
	"github.com/pterm/pterm/putils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/tidwall/pretty"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	filterTypes   []string
	filterPubKeys []string
	showLogs      bool
	logger        = logging.MustGetLogger("skywire-cli")
	removeAll     bool
	tpTypes       bool
)

func init() {
	tpCmd.Flags().SortFlags = false
	addTpCmd.Flags().SortFlags = false
	rmTpCmd.Flags().SortFlags = false
	discTpCmd.Flags().SortFlags = false
	treeCmd.Flags().SortFlags = false
	tpCmd.AddCommand(
		addTpCmd,
		rmTpCmd,
		discTpCmd,
		treeCmd,
	)
	tpCmd.Flags().StringSliceVarP(&filterTypes, "types", "t", filterTypes, "show transport(s) type(s) comma-separated")
	tpCmd.Flags().StringSliceVarP(&filterPubKeys, "pks", "p", filterPubKeys, "show transport(s) for public key(s) comma-separated")
	tpCmd.Flags().BoolVarP(&showLogs, "logs", "l", true, "show transport logs")
	tpCmd.Flags().StringVarP(&tpID, "id", "i", "", "display transport matching ID")
	tpCmd.Flags().BoolVarP(&tpTypes, "tptypes", "u", false, "display transport types used by the local visor")
	tpCmd.Flags().StringVar(&clirpc.Addr, "rpc", "localhost:3435", "RPC server address")
	addTpCmd.Flags().StringVar(&clirpc.Addr, "rpc", "localhost:3435", "RPC server address")
	rmTpCmd.Flags().StringVar(&clirpc.Addr, "rpc", "localhost:3435", "RPC server address")
}

// RootCmd contains commands that interact with the skywire-visor
var tpCmd = &cobra.Command{
	Use:   "tp",
	Short: "View and manage transports",
	Long: `Display and manage transports of the local visor

	Transports are bidirectional communication protocols
	used between two Skywire Visors (or Transport Edges)

	Each Transport is represented as a unique 16 byte (128 bit)
	UUID value called the Transport ID
	and has a Transport Type that identifies
	a specific implementation of the Transport.

	Types: stcp stcpr sudph dmsg`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		if tpTypes {
			types, err := rpcClient.TransportTypes()
			internal.Catch(cmd.Flags(), err)
			internal.PrintOutput(cmd.Flags(), types, fmt.Sprintln(strings.Join(types, "\n")))
			return
		}

		if tpID != "" {
			tpid := internal.ParseUUID(cmd.Flags(), "transport-id", tpID)
			tp, err := rpcClient.Transport(tpid)
			internal.Catch(cmd.Flags(), err)
			PrintTransports(cmd.Flags(), tp)
			return
		}

		var pks cipher.PubKeys
		if filterPubKeys != nil {
			internal.Catch(cmd.Flags(), pks.Set(strings.Join(filterPubKeys, ",")))
		}
		transports, err := rpcClient.Transports(filterTypes, pks, showLogs)
		internal.Catch(cmd.Flags(), err)
		PrintTransports(cmd.Flags(), transports...)
	},
}

var (
	sortedEdgeKeys []string
	utURL          string
	tpdURL         string
	rootNode       string
	lastNode       string
	rootnode       cipher.PubKey
	lastnode       cipher.PubKey
	cacheFileTPD   string
	cacheFileUT    string
	padSpaces      int
	isStats        bool
	rawData        bool
	refinedData    bool
	noFilterOnline bool
	onlyOnline     bool
	transportType  string
	timeout        time.Duration
	rpk            string
	sdURL          string
	cacheFileSD    string
	cacheFilesAge  int
	forceAttempt   bool

// queryHealth	bool
)

func init() {
	addTpCmd.Flags().StringVarP(&rpk, "rpk", "r", "", "remote public key.")
	addTpCmd.Flags().StringVarP(&transportType, "type", "t", "", "type of transport to add.")
	addTpCmd.Flags().DurationVarP(&timeout, "timeout", "o", 0, "if specified, sets an operation timeout")
	addTpCmd.Flags().StringVarP(&sdURL, "sdurl", "a", utilenv.ServiceDiscAddr, "service discovery url")
	//TODO
	//	listCmd.Flags().BoolVarP(&queryHealth, "health", "q", false, "check /health of remote visor over dmsg before creating transport")
	addTpCmd.Flags().BoolVarP(&forceAttempt, "force", "f", false, "attempt transport creation without check of SD") // or visor /health over dmsg
	addTpCmd.Flags().StringVar(&cacheFileSD, "cfs", os.TempDir()+"/pvisorsd.json", "SD cache file location")
	addTpCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes")
}

var addTpCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a transport",
	Long: `
    Add a transport
		If the transport type is unspecified,
		the visor will attempt to establish a transport
		in the following order: stcpr, sudph, dmsg`,
	Args:                  cobra.MinimumNArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		isJSON, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		var pk cipher.PubKey

		if rpk == "" {
			pk = internal.ParsePK(cmd.Flags(), "remote-public-key", args[0])
		} else {
			internal.Catch(cmd.Flags(), pk.Set(rpk))
		}

		var pvs string
		var pvkeys []string
		var foundPV bool
		//check before connecting stcpr transport that the visor is in the public visor list unless forceAttempt == true
		if (transportType == "" || transportType == "stcpr") && !forceAttempt {
			pvs = internal.GetData(cacheFileSD, sdURL+"/api/services?type=visor", cacheFilesAge)
			pvkeys, _ = script.Echo(pvs).JQ(".[].address").Replace(":", " ").Column(1).Slice() //nolint
			for _, pvkey := range pvkeys {
				if pk.String() == pvkey {
					foundPV = true
					break
				}
			}
		}

		if transportType == "stcpr" && !forceAttempt && !foundPV {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("cannot create stcpr transport ; public key not found in public visor service discovery.\nUse -f --force to force attempt transport creation"))
		}

		var tp *visor.TransportSummary

		if transportType != "" {
			tp, err = rpcClient.AddTransport(pk, transportType, timeout)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to establish %v transport: %v", transportType, err))
			}
			if !isJSON {
				logger.Infof("Established %v transport to %v", transportType, pk)
			}
		} else {
			var transportTypes []network.Type
			if foundPV {
				transportTypes = []network.Type{
					network.STCPR,
					network.SUDPH,
					network.DMSG,
				}
			} else {
				transportTypes = []network.Type{
					network.SUDPH,
					network.DMSG,
				}
			}
			for _, transportType := range transportTypes {
				tp, err = rpcClient.AddTransport(pk, string(transportType), timeout)
				if err == nil {
					if !isJSON {
						logger.Infof("Established %v transport to %v", transportType, pk)
					}
					break
				}
				if !isJSON {
					logger.WithError(err).Warnf("Failed to establish %v transport", transportType)
				}
			}
		}
		PrintTransports(cmd.Flags(), tp)
	},
}

func init() {
	rmTpCmd.Flags().BoolVarP(&removeAll, "all", "a", false, "remove all transports")
	rmTpCmd.Flags().StringVarP(&tpID, "id", "i", "", "remove transport of given ID")
}

var rmTpCmd = &cobra.Command{
	Use:                   "rm",
	Short:                 "Remove transport(s) by id",
	Long:                  "\n    Remove transport(s) by id",
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if removeAll {
			internal.Catch(cmd.Flags(), rpcClient.RemoveAllTransports())
			internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
		} else if tpID != "" {
			tID := internal.ParseUUID(cmd.Flags(), "transport-id", tpID)
			if err != nil {
				os.Exit(1)
			}
			internal.Catch(cmd.Flags(), rpcClient.RemoveTransport(tID))
			internal.PrintOutput(cmd.Flags(), "OK", "OK\n")
		} else {
			internal.PrintOutput(cmd.Flags(), "", cmd.Help())
		}
	},
}

// PrintTransports prints transports used by the visor
func PrintTransports(cmdFlags *pflag.FlagSet, tps ...*visor.TransportSummary) {
	sortTransports(tps...)

	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 5, ' ', tabwriter.TabIndent)
	_, err := fmt.Fprintln(w, "type\tid\tremote_pk\tmode\tlabel")
	internal.Catch(cmdFlags, err)

	type outputTP struct {
		Type   network.Type    `json:"type"`
		ID     uuid.UUID       `json:"id"`
		Remote cipher.PubKey   `json:"remote_pk"`
		TpMode string          `json:"mode"`
		Label  transport.Label `json:"label"`
	}

	var outputTPS []outputTP

	for _, tp := range tps {
		tpMode := "regular"
		if tp.IsSetup {
			tpMode = "setup"
		}
		tp.Log = nil
		oTP := outputTP{
			Type:   tp.Type,
			ID:     tp.ID,
			Remote: tp.Remote,
			TpMode: tpMode,
			Label:  tp.Label,
		}
		outputTPS = append(outputTPS, oTP)

		_, err = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", tp.Type, tp.ID, tp.Remote, tpMode, tp.Label)
		internal.Catch(cmdFlags, err)
	}
	internal.Catch(cmdFlags, w.Flush())
	internal.PrintOutput(cmdFlags, outputTPS, b.String())
}

func sortTransports(tps ...*visor.TransportSummary) {
	sort.Slice(tps, func(i, j int) bool {
		return tps[i].ID.String() < tps[j].ID.String()
	})
}

var (
	tpID string
	tpPK string
)

func init() {
	discTpCmd.Flags().StringVarP(&tpID, "id", "i", "", "obtain transport of given ID")
	discTpCmd.Flags().StringVarP(&tpPK, "pk", "p", "", "obtain transports by public key")
}

var discTpCmd = &cobra.Command{
	Use:                   "disc",
	Short:                 "Discover remote transport(s)",
	Long:                  "\n    Discover remote transport(s) by ID or public key",
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		if tpID == "" && tpPK == "" {
			internal.PrintFatalError(cmd.Flags(), errors.New("must specify either transport id or public key"))
			return
		}
		if tpID != "" && tpPK != "" {
			internal.PrintFatalError(cmd.Flags(), errors.New("cannot specify both transport id and public key"))
			return
		}
		var tppk cipher.PubKey
		var tpid transportID
		if tpID != "" {
			internal.Catch(cmd.Flags(), tpid.Set(tpID))
		}
		if tpPK != "" {
			internal.Catch(cmd.Flags(), tppk.Set(tpPK))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		if tppk.Null() {
			entry, err := rpcClient.DiscoverTransportByID(uuid.UUID(tpid))
			internal.Catch(cmd.Flags(), err)
			PrintTransportEntries(cmd.Flags(), entry)
		} else {
			entries, err := rpcClient.DiscoverTransportsByPK(tppk)
			internal.Catch(cmd.Flags(), err)
			PrintTransportEntries(cmd.Flags(), entries...)
		}
	},
}

// PrintTransportEntries prints the transport entries
func PrintTransportEntries(cmdFlags *pflag.FlagSet, entries ...*transport.Entry) {

	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 5, ' ', tabwriter.TabIndent)
	_, err := fmt.Fprintln(w, "id\ttype\tedge1\tedge2")
	internal.Catch(cmdFlags, err)

	type outputEntry struct {
		ID    uuid.UUID     `json:"id"`
		Type  network.Type  `json:"type"`
		Edge1 cipher.PubKey `json:"edge1"`
		Edge2 cipher.PubKey `json:"edge2"`
	}

	var outputEntries []outputEntry
	for _, e := range entries {
		_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			e.ID, e.Type, e.Edges[0], e.Edges[1])
		internal.Catch(cmdFlags, err)
		oEntry := outputEntry{
			ID:    e.ID,
			Type:  e.Type,
			Edge1: e.Edges[0],
			Edge2: e.Edges[1],
		}
		outputEntries = append(outputEntries, oEntry)
	}
	internal.Catch(cmdFlags, w.Flush())
	internal.PrintOutput(cmdFlags, outputEntries, b.String())
}

type transportID uuid.UUID

// String implements pflag.Value
func (t transportID) String() string { return uuid.UUID(t).String() }

// Type implements pflag.Value
func (transportID) Type() string { return "transportID" }

// Set implements pflag.Value
func (t *transportID) Set(s string) error {
	tID, err := uuid.Parse(s)
	if err != nil {
		return err
	}
	*t = transportID(tID)
	return nil
}

// RootCmd is tpCmd
var RootCmd = tpCmd

func init() {

	treeCmd.Flags().StringVarP(&rootNode, "source", "k", "", "root node ; defaults to visor with most transports")
	treeCmd.Flags().StringVarP(&lastNode, "dest", "d", "", "map route between source and dest")
	treeCmd.Flags().StringVarP(&tpdURL, "tpdurl", "a", utilenv.TpDiscAddr, "transport discovery url")
	treeCmd.Flags().StringVarP(&utURL, "uturl", "w", utilenv.UptimeTrackerAddr, "uptime tracker url")
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
	Long:  fmt.Sprintf("display a tree representation of transports from TPD\n\n%v/all-transports\n\nSet cache file location to \"\" to avoid using cache files", utilenv.TpDiscAddr),
	Run: func(cmd *cobra.Command, args []string) {
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
					pterm.DefaultTree.WithRoot(putils.TreeFromLeveledList(leveledList)).Render() //nolint
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
			l, _ := script.Echo(tps).JQ("[.[] | select(.edges | contains([" + sortedEdgeKeys[0] + "," + sortedEdgeKeys[1] + "]))]").Slice() //nolint
			if len(l) > 0 && fmt.Sprintf("%v", l) != "[[]]" {
				pterm.Println(pterm.Red("Direct route:"))
				for _, m := range l {
					script.Echo(string(pretty.Color(pretty.Pretty([]byte(m)), nil))).Stdout() //nolint
				}
				return
			}
			var routeSlice []string
			var listLevel int
			re := regexp.MustCompile(`\s+`)
			for i := len(leveledList) - 1; i >= 0; i-- {
				if len(routeSlice) == 0 && strings.Contains(leveledList[i].Text, lastnode.String()) {
					rStepTpid, _ := script.Echo(fmt.Sprintf("%v", leveledList[i].Text)).ReplaceRegexp(re, " ").Column(2).Replace("\n", "").String()       //nolint
					rStepTp, _ := script.Echo(tps).JQ(".[] | select(.t_id == "+`"`+strings.TrimRight(rStepTpid, "\n")+`"`+")").Replace("\n", "").String() //nolint
					routeSlice = append(routeSlice, rStepTp)
					listLevel = leveledList[i].Level
				}
				if len(routeSlice) > 0 && leveledList[i].Level == (listLevel-1) {
					rStepTpid, _ := script.Echo(fmt.Sprintf("%v", leveledList[i].Text)).ReplaceRegexp(re, " ").Column(2).Replace("\n", "").String()       //nolint
					rStepTp, _ := script.Echo(tps).JQ(".[] | select(.t_id == "+`"`+strings.TrimRight(rStepTpid, "\n")+`"`+")").Replace("\n", "").String() //nolint
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
							l, _ := script.Echo(tps).JQ(".[] | select(.edges[] == " + v + ") | .edges[] | select(. != " + v + ")").Slice() //nolint
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
			l, _ := script.Echo(tps).JQ(".[] | select(.edges[0] == .edges[1]) | .edges[0] + \""+strings.Repeat(" ", padSpaces)+"\" + .t_id + \" \" + .type").Replace("\"", "").Slice() //nolint
			if len(l) > 0 {
				pterm.Println(pterm.Red("Self-transports"))
				for _, m := range l {
					pterm.Println(filterOnlineStatus(utkeys, offlinekeys, m))
				}
			}
		}
	},
}

/*
	type tpdMap struct {
		PK    string
		Edges []string
	}

type tpdMaps []tpdMap
*/
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
