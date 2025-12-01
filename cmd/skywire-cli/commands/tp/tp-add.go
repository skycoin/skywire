// Package clitp cmd/skywire-cli/commands/tp/tp-add.go
package clitp

import (
	"fmt"
	"os"
	"time"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	sortedEdgeKeys   []string
	tpdURL           string
	rootNode         string
	lastNode         string
	rootnode         cipher.PubKey
	lastnode         cipher.PubKey
	cacheFileTPD     string
	cacheFileDmsgD   string
	cacheFileUT      string
	cacheFileSDProxy string
	cacheFileSDVPN   string
	padSpaces        int
	isStats          bool
	rawData          bool
	refinedData      bool
	noFilterOnline   bool
	onlyOnline       bool
	transportType    string
	timeout          time.Duration
	rpk              string
	cacheFileAR      string
	cacheFilesAge    int
	forceAttempt     bool
	arURL            string
	dmsgdURL         string

// queryHealth	bool
)

func init() {
	addTpCmd.Flags().StringVarP(&rpk, "rpk", "r", "", "remote public key.")
	addTpCmd.Flags().StringVarP(&transportType, "type", "t", "", "type of transport to add.")
	addTpCmd.Flags().DurationVarP(&timeout, "timeout", "o", 0, "if specified, sets an operation timeout")
	addTpCmd.Flags().StringVarP(&arURL, "ar", "a", deployment.Prod.AddressResolver, "address resolver URL")
	addTpCmd.Flags().StringVarP(&dmsgdURL, "dmsg", "d", deployment.Prod.DmsgDiscovery, "dmsg discovery URL")
	//TODO
	//	listCmd.Flags().BoolVarP(&queryHealth, "health", "q", false, "check /health of remote visor over dmsg before creating transport")
	addTpCmd.Flags().BoolVarP(&forceAttempt, "force", "f", false, "attempt transport creation without check of SD") // or visor /health over dmsg
	addTpCmd.Flags().StringVar(&cacheFileAR, "cfar", os.TempDir()+"/ar.json", "AR cache file location")
	addTpCmd.Flags().StringVar(&cacheFileDmsgD, "cfdd", os.TempDir()+"/dmsgd.json", "Dmsg Discovery cache file location")
	addTpCmd.Flags().IntVarP(&cacheFilesAge, "cfa", "m", 5, "update cache files if older than n minutes")
	addTpCmd.Flags().StringVar(&clirpc.Addr, "rpc", "localhost:3435", "RPC server address")
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
		if transportType != "dmsg" && transportType != "stcpr" && transportType != "sudph" && transportType != "" {
			logger.Fatal("Invalid transport type specified:", transportType)
		}
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

		var transports string
		var dmsgEntries string
		var stcprkeys []string
		var sudphkeys []string
		var dmsgkeys []string
		var availableSTCPR bool
		var availableSUDPH bool
		var availableDMSG bool
		//check before connecting stcpr transport that the visor public key is available to be transported via the given transport type unless forceAttempt == true
		if !forceAttempt {
			transports = internal.GetData(cacheFileAR, arURL+"/transports", cacheFilesAge)
			stcprkeys, _ = script.Echo(transports).JQ(".stcpr[]").Replace(`"`, "").Slice() //nolint:errcheck
			if transportType == "stcpr" {
				found := false
				for i := range stcprkeys {
					if pk.String() == stcprkeys[i] {
						found = true
						break
					}
				}
				if !found {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("cannot create stcpr transport ; public key not found in address resolver %v/transports.\nUse -f --force to force attempt transport creation", arURL))
				} else {
					availableSTCPR = true
				}
			}
			sudphkeys, _ = script.Echo(transports).JQ(".sudph[]").Replace(`"`, "").Slice() //nolint:errcheck
			if transportType == "sudph" {
				found := false
				for i := range sudphkeys {
					if pk.String() == sudphkeys[i] {
						found = true
						break
					}
				}
				if !found {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("cannot create sudph transport ; public key not found in address resolver %v/transports.\nUse -f --force to force attempt transport creation", arURL))
				} else {
					availableSUDPH = true
				}
			}
			dmsgEntries = internal.GetData(cacheFileDmsgD, dmsgdURL+"/dmsg-discovery/entries", cacheFilesAge)
			dmsgkeys, _ = script.Echo(dmsgEntries).JQ(".[]").Replace(`"`, "").Slice() //nolint:errcheck
			if transportType == "dmsg" {
				found := false
				for i := range dmsgkeys {
					if pk.String() == dmsgkeys[i] {
						found = true
						break
					}
				}
				if !found {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("cannot create dmsg transport ; public key not found in dmsg discovery entries %v/dmsg-discovery/entries.\nUse -f --force to force attempt transport creation", dmsgdURL))
				} else {
					availableDMSG = true
				}
			}
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
			var transportTypes []types.Type
			if forceAttempt {
				transportTypes = []types.Type{
					types.STCPR,
					types.SUDPH,
					types.DMSG,
				}
			} else {
				if availableSTCPR {
					transportTypes = append(transportTypes, types.STCPR)
				}
				if availableSUDPH {
					transportTypes = append(transportTypes, types.SUDPH)
				}
				if availableDMSG {
					transportTypes = append(transportTypes, types.DMSG)
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
