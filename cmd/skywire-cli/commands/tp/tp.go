// Package clitp cmd/skywire-cli/commands/tp/tp.go
package clitp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	filterTypes      []string
	filterPubKeys    []string
	showLogs         bool
	showMore         bool
	bwDays           int
	showInactive     bool
	logger           = logging.MustGetLogger("skywire-cli")
	tpTypes          bool
	showStats        bool
	utURL            string
	sdURL            string
	listRemoteVisors []string
	// RootCmd is tpCmd
	RootCmd = tpCmd
)

func init() {
	tpCmd.Flags().SortFlags = false
	addTpCmd.Flags().SortFlags = false
	rmTpCmd.Flags().SortFlags = false
	discTpCmd.Flags().SortFlags = false
	treeCmd.Flags().SortFlags = false
	vizCmd.Flags().SortFlags = false
	autoCmd.Flags().SortFlags = false
	metricsCmd.Flags().SortFlags = false
	tpCmd.AddCommand(
		addTpCmd,
		rmTpCmd,
		discTpCmd,
		treeCmd,
		visorListCmd,
		vizCmd,
		autoCmd,
		syncCmd,
		metricsCmd,
	)
	tpCmd.Flags().StringSliceVarP(&filterTypes, "types", "t", filterTypes, "show transport(s) type(s) comma-separated")
	tpCmd.Flags().StringSliceVarP(&filterPubKeys, "pks", "p", filterPubKeys, "show transport(s) for public key(s) comma-separated")
	tpCmd.Flags().BoolVarP(&showLogs, "logs", "l", true, "show transport logs")
	tpCmd.Flags().BoolVarP(&showMore, "more", "m", false, "show more info")
	tpCmd.Flags().IntVarP(&bwDays, "bw", "b", 0, "show bandwidth usage for last N days (0 = disabled)")
	tpCmd.Flags().BoolVar(&showInactive, "inactive", false, "show bandwidth for inactive transports (requires --bw)")
	tpCmd.Flags().StringVar(&cacheFileUT, "cfu", os.TempDir()+"/ut.json", "UT cache file location.")
	tpCmd.Flags().StringVar(&cacheFileSDProxy, "cfsp", os.TempDir()+"/proxysd.json", "SD cache file location")
	tpCmd.Flags().StringVar(&cacheFileSDVPN, "cfsv", os.TempDir()+"/vpnsd.json", "SD cache file location")
	tpCmd.Flags().StringVar(&cacheFileSDVisor, "cfsvisor", os.TempDir()+"/visorsd.json", "SD cache file location")
	tpCmd.Flags().StringVarP(&sdURL, "sdurl", "a", deployment.Prod.ServiceDiscovery, "service discovery url")
	tpCmd.Flags().StringVarP(&utURL, "uturl", "w", deployment.Prod.UptimeTracker, "uptime tracker url")
	tpCmd.Flags().StringVarP(&tpID, "id", "i", "", "display transport matching ID")
	tpCmd.Flags().BoolVarP(&tpTypes, "tptypes", "u", false, "display transport types used by the local visor")
	tpCmd.Flags().BoolVarP(&showStats, "stats", "s", false, "show transport statistics (count by type, unique visors)")
	tpCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")
	tpCmd.Flags().StringSliceVar(&listRemoteVisors, "remote", nil, "list transports on remote visor(s) via TPS (comma-separated PKs)")
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

	Types: stcp stcpr sudph dmsg
`,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		// Handle --remote flag: list transports on remote visor(s) via TPS
		if len(listRemoteVisors) > 0 {
			isJSON, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck

			// Parse all target PKs
			var targetPKs []cipher.PubKey
			for _, pkStr := range listRemoteVisors {
				var pk cipher.PubKey
				if err := pk.Set(pkStr); err != nil {
					internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid public key %q: %w", pkStr, err))
				}
				targetPKs = append(targetPKs, pk)
			}

			// Check if embedded TPS is running - if so, use only embedded TPS
			// If embedded TPS is not running, use external TPS nodes
			tpsStatus, tpsStatusErr := rpcClient.TPSStatus()
			useEmbeddedTPS := tpsStatusErr == nil && tpsStatus != nil && tpsStatus.Enabled

			// Get TPS nodes once (for external TPS fallback)
			var tpsNodes []cipher.PubKey
			var tpsNodesErr error

			// Process and print results with streaming output
			type remoteResult struct {
				TargetPK   cipher.PubKey
				Transports []visor.TPSTransportResponse
				Error      string
			}
			var allResults []remoteResult // for JSON output

			for i, targetPK := range targetPKs {
				var tpsTransports []visor.TPSTransportResponse
				var tpErr error

				if useEmbeddedTPS {
					// Use embedded TPS only - if it fails, don't try external nodes
					tpsTransports, tpErr = rpcClient.TPSGetTransports(targetPK)
				} else {
					// Embedded TPS not running - use external TPS nodes
					// Lazily get TPS nodes on first use
					if tpsNodes == nil && tpsNodesErr == nil {
						tpsNodes, tpsNodesErr = rpcClient.GetTransportSetupNodesSorted()
					}

					if tpsNodesErr != nil || len(tpsNodes) == 0 {
						result := remoteResult{TargetPK: targetPK, Error: "no TPS nodes available"}
						allResults = append(allResults, result)
						if !isJSON {
							fmt.Printf("[%d/%d] %s (error: %s)\n", i+1, len(targetPKs), targetPK.String(), result.Error)
						}
						continue
					}

					// Try each TPS node (already sorted by health, healthy first)
					for _, tpsPK := range tpsNodes {
						logger.Debugf("Trying TPS node %s for target %s", tpsPK.String()[:16], targetPK.String()[:16])

						// Health check
						if err := rpcClient.TPSExternalHealthCheck(tpsPK); err != nil {
							logger.WithError(err).Debugf("TPS %s health check failed", tpsPK.String()[:16])
							continue
						}

						// Try to get transports via this TPS
						tpsTransports, tpErr = rpcClient.TPSExternalGetTransports(tpsPK, targetPK)
						if tpErr == nil {
							logger.Debugf("Got transports from %s via TPS %s", targetPK.String()[:16], tpsPK.String()[:16])
							break
						}
						logger.WithError(tpErr).Debugf("TPS %s failed to get transports for %s", tpsPK.String()[:16], targetPK.String()[:16])
					}
				}

				var result remoteResult
				if tpErr != nil {
					result = remoteResult{TargetPK: targetPK, Error: tpErr.Error()}
				} else {
					result = remoteResult{TargetPK: targetPK, Transports: tpsTransports}
				}
				allResults = append(allResults, result)

				// Stream output immediately (non-JSON mode)
				if !isJSON {
					if result.Error != "" {
						fmt.Printf("[%d/%d] %s (error: %s)\n", i+1, len(targetPKs), targetPK.String(), result.Error)
					} else {
						fmt.Printf("[%d/%d] %s (%d transports)\n", i+1, len(targetPKs), targetPK.String(), len(result.Transports))
						if len(result.Transports) > 0 {
							var b bytes.Buffer
							w := tabwriter.NewWriter(&b, 0, 0, 3, ' ', tabwriter.TabIndent)
							fmt.Fprintln(w, "  type\tid\tlocal\tremote") //nolint:errcheck
							for _, tp := range result.Transports {
								fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", tp.Type, tp.ID, tp.Local, tp.Remote) //nolint:errcheck
							}
							w.Flush() //nolint:errcheck,gosec
							fmt.Print(b.String())
						}
					}
				}
			}

			// Print JSON output at the end if requested
			if isJSON {
				internal.PrintOutput(cmd.Flags(), allResults, "")
			}
			return
		}

		if tpTypes {
			types, err := rpcClient.TransportTypes()
			internal.Catch(cmd.Flags(), err)
			internal.PrintOutput(cmd.Flags(), types, fmt.Sprintln(strings.Join(types, "\n")))
			return
		}

		if showStats {
			transports, err := rpcClient.Transports(filterTypes, nil, false)
			internal.Catch(cmd.Flags(), err)

			byType := make(map[string]int)
			uniqueVisors := make(map[cipher.PubKey]struct{})
			for _, tp := range transports {
				byType[string(tp.Type)]++
				uniqueVisors[tp.Remote] = struct{}{}
			}

			type statsOutput struct {
				Total        int            `json:"total"`
				UniqueVisors int            `json:"unique_visors"`
				ByType       map[string]int `json:"by_type"`
			}
			stats := statsOutput{
				Total:        len(transports),
				UniqueVisors: len(uniqueVisors),
				ByType:       byType,
			}

			var b strings.Builder
			fmt.Fprintf(&b, "Total transports:  %d\n", stats.Total)
			fmt.Fprintf(&b, "Unique visors:     %d\n", stats.UniqueVisors)
			// Sort type names for consistent output
			typeNames := make([]string, 0, len(byType))
			for t := range byType {
				typeNames = append(typeNames, t)
			}
			sort.Strings(typeNames)
			for _, t := range typeNames {
				fmt.Fprintf(&b, "  %-10s %d\n", t+":", byType[t])
			}

			internal.PrintOutput(cmd.Flags(), stats, b.String())
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

		if showMore {
			utData = internal.GetData(cacheFileUT, utURL+"/uptimes?v=v2", cacheFilesAge)
			proxyData = internal.GetData(cacheFileSDProxy, sdURL+"/api/services?type="+servicedisc.ServiceTypeProxy, cacheFilesAge)
			vpnData = internal.GetData(cacheFileSDVPN, sdURL+"/api/services?type="+servicedisc.ServiceTypeVPN, cacheFilesAge)
			visorData = internal.GetData(cacheFileSDVisor, sdURL+"/api/services?type="+servicedisc.ServiceTypeVisor, cacheFilesAge)
		}

		// Get transport logs for bandwidth display
		var logEntries []visor.TransportLogEntry
		bwByTpID := make(map[string]struct{ recv, sent uint64 })
		if bwDays > 0 {
			logEntries, err = rpcClient.GetTransportLogs(bwDays)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to get transport logs: %v\n", err)
			} else if len(logEntries) == 0 {
				fmt.Fprintf(os.Stderr, "Note: No transport log entries found for the last %d day(s)\n", bwDays)
			}
			// Aggregate bandwidth by tpID (sum across all days)
			for _, entry := range logEntries {
				existing := bwByTpID[entry.TpID.String()]
				existing.recv += entry.RecvBytes
				existing.sent += entry.SentBytes
				bwByTpID[entry.TpID.String()] = existing
			}
		}

		// If showing inactive transports, find those in logs but not in current transports
		var inactiveTransports []inactiveTransport
		if showInactive && bwDays > 0 {
			// Build set of active transport IDs
			activeIDs := make(map[string]bool)
			for _, tp := range transports {
				activeIDs[tp.ID.String()] = true
			}

			// Find inactive transport IDs from logs
			for _, entry := range logEntries {
				tpIDStr := entry.TpID.String()
				if activeIDs[tpIDStr] {
					continue
				}
				bw := bwByTpID[tpIDStr]
				inactiveTransports = append(inactiveTransports, inactiveTransport{
					ID:        entry.TpID,
					RecvBytes: bw.recv,
					SentBytes: bw.sent,
				})
			}
		}

		PrintTransportsWithBandwidth(cmd.Flags(), bwByTpID, inactiveTransports, transports...)
	},
}

// inactiveTransport represents a transport that is no longer active but has bandwidth history
type inactiveTransport struct {
	ID        uuid.UUID
	RecvBytes uint64
	SentBytes uint64
}

var (
	utData    string
	vpnData   string
	proxyData string
	visorData string
)

// PrintTransports prints transports used by the visor
func PrintTransports(cmdFlags *pflag.FlagSet, tps ...*visor.TransportSummary) {
	sortTransports(tps...)

	var versionsByPK map[string]string
	if showMore && len(utData) > 0 {
		type uptimeEntry struct {
			PK      string `json:"pk"`
			Version string `json:"version"`
		}
		var utEntries []uptimeEntry
		err := json.Unmarshal([]byte(utData), &utEntries)
		internal.Catch(cmdFlags, err)

		versionsByPK = make(map[string]string, len(utEntries))
		for _, e := range utEntries {
			versionsByPK[e.PK] = e.Version
		}
	}

	type geoInfo struct {
		Country string `json:"country"`
	}

	type serviceEntry struct {
		Address string  `json:"address"`
		Geo     geoInfo `json:"geo"`
	}

	proxyByPK := make(map[string]string)
	vpnByPK := make(map[string]string)
	visorByPK := make(map[string]string)

	if showMore && len(proxyData) > 0 {
		var proxyEntries []serviceEntry
		err := json.Unmarshal([]byte(proxyData), &proxyEntries)
		internal.Catch(cmdFlags, err)
		for _, e := range proxyEntries {
			pk := strings.SplitN(e.Address, ":", 2)[0]
			proxyByPK[pk] = e.Geo.Country
		}
	}

	if showMore && len(vpnData) > 0 {
		var vpnEntries []serviceEntry
		err := json.Unmarshal([]byte(vpnData), &vpnEntries)
		internal.Catch(cmdFlags, err)
		for _, e := range vpnEntries {
			pk := strings.SplitN(e.Address, ":", 2)[0]
			vpnByPK[pk] = e.Geo.Country
		}
	}

	if showMore && len(visorData) > 0 {
		var visorEntries []serviceEntry
		err := json.Unmarshal([]byte(visorData), &visorEntries)
		internal.Catch(cmdFlags, err)
		for _, e := range visorEntries {
			pk := strings.SplitN(e.Address, ":", 2)[0]
			visorByPK[pk] = e.Geo.Country
		}
	}

	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 5, ' ', tabwriter.TabIndent)

	if showMore && bwDays > 0 {
		_, err := fmt.Fprintln(w, "type\tid\tremote_pk\tmode\tlabel\tversion\tcountry\tservices\trecv\tsent")
		internal.Catch(cmdFlags, err)
	} else if showMore {
		_, err := fmt.Fprintln(w, "type\tid\tremote_pk\tmode\tlabel\tversion\tcountry\tservices")
		internal.Catch(cmdFlags, err)
	} else if bwDays > 0 {
		_, err := fmt.Fprintln(w, "type\tid\tremote_pk\tmode\tlabel\trecv\tsent")
		internal.Catch(cmdFlags, err)
	} else {
		_, err := fmt.Fprintln(w, "type\tid\tremote_pk\tmode\tlabel")
		internal.Catch(cmdFlags, err)
	}

	type outputTP struct {
		Type      types.Type      `json:"type"`
		ID        uuid.UUID       `json:"id"`
		Remote    cipher.PubKey   `json:"remote_pk"`
		TpMode    string          `json:"mode"`
		Label     transport.Label `json:"label"`
		Version   string          `json:"version,omitempty"`
		Country   string          `json:"country,omitempty"`
		Services  string          `json:"services,omitempty"`
		RecvBytes uint64          `json:"recv_bytes,omitempty"`
		SentBytes uint64          `json:"sent_bytes,omitempty"`
	}

	var outputTPS []outputTP
	for _, tp := range tps {
		if tp == nil {
			continue
		}
		tpMode := "regular"
		if tp.IsSetup {
			tpMode = "setup"
		}

		// Extract bandwidth before clearing log
		var recvBytes, sentBytes uint64
		if tp.Log != nil {
			if tp.Log.RecvBytes != nil {
				recvBytes = *tp.Log.RecvBytes
			}
			if tp.Log.SentBytes != nil {
				sentBytes = *tp.Log.SentBytes
			}
		}
		tp.Log = nil

		version := ""
		if showMore && versionsByPK != nil {
			version = versionsByPK[tp.Remote.String()]
		}

		var country, services string
		pk := tp.Remote.String()
		proxyCountry, inProxy := proxyByPK[pk]
		vpnCountry, inVPN := vpnByPK[pk]
		visorCountry, inVisor := visorByPK[pk]

		// Build services list
		var svcList []string
		var countries []string

		if inProxy {
			svcList = append(svcList, "proxy")
			countries = append(countries, proxyCountry)
		}
		if inVPN {
			svcList = append(svcList, "vpn")
			countries = append(countries, vpnCountry)
		}
		if inVisor {
			svcList = append(svcList, "visor")
			countries = append(countries, visorCountry)
		}

		if len(svcList) > 0 {
			services = strings.Join(svcList, ",")
			// Deduplicate countries
			countryMap := make(map[string]bool)
			var uniqueCountries []string
			for _, c := range countries {
				if c != "" && !countryMap[c] {
					countryMap[c] = true
					uniqueCountries = append(uniqueCountries, c)
				}
			}
			country = strings.Join(uniqueCountries, "/")
		}

		oTP := outputTP{
			Type:      tp.Type,
			ID:        tp.ID,
			Remote:    tp.Remote,
			TpMode:    tpMode,
			Label:     tp.Label,
			Version:   version,
			Country:   country,
			Services:  services,
			RecvBytes: recvBytes,
			SentBytes: sentBytes,
		}
		outputTPS = append(outputTPS, oTP)

		if showMore && bwDays > 0 {
			_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				tp.Type, tp.ID, tp.Remote, tpMode, tp.Label, version, country, services,
				formatBytes(recvBytes), formatBytes(sentBytes))
			internal.Catch(cmdFlags, err)
		} else if showMore {
			_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				tp.Type, tp.ID, tp.Remote, tpMode, tp.Label, version, country, services)
			internal.Catch(cmdFlags, err)
		} else if bwDays > 0 {
			_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				tp.Type, tp.ID, tp.Remote, tpMode, tp.Label,
				formatBytes(recvBytes), formatBytes(sentBytes))
			internal.Catch(cmdFlags, err)
		} else {
			_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				tp.Type, tp.ID, tp.Remote, tpMode, tp.Label)
			internal.Catch(cmdFlags, err)
		}
	}

	internal.Catch(cmdFlags, w.Flush())
	internal.PrintOutput(cmdFlags, outputTPS, b.String())
}

func sortTransports(tps ...*visor.TransportSummary) {
	sort.Slice(tps, func(i, j int) bool {
		return tps[i].ID.String() < tps[j].ID.String()
	})
}

// formatBytes formats bytes in human-readable format (KB, MB, GB)
func formatBytes(bytes uint64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2fGB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2fMB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2fKB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

// PrintTransportsWithBandwidth prints transports with bandwidth data from logs
func PrintTransportsWithBandwidth(cmdFlags *pflag.FlagSet, bwByTpID map[string]struct{ recv, sent uint64 }, inactive []inactiveTransport, tps ...*visor.TransportSummary) {
	sortTransports(tps...)

	var versionsByPK map[string]string
	if showMore && len(utData) > 0 {
		type uptimeEntry struct {
			PK      string `json:"pk"`
			Version string `json:"version"`
		}
		var utEntries []uptimeEntry
		err := json.Unmarshal([]byte(utData), &utEntries)
		internal.Catch(cmdFlags, err)

		versionsByPK = make(map[string]string, len(utEntries))
		for _, e := range utEntries {
			versionsByPK[e.PK] = e.Version
		}
	}

	type geoInfo struct {
		Country string `json:"country"`
	}

	type serviceEntry struct {
		Address string  `json:"address"`
		Geo     geoInfo `json:"geo"`
	}

	proxyByPK := make(map[string]string)
	vpnByPK := make(map[string]string)
	visorByPK := make(map[string]string)

	if showMore && len(proxyData) > 0 {
		var proxyEntries []serviceEntry
		err := json.Unmarshal([]byte(proxyData), &proxyEntries)
		internal.Catch(cmdFlags, err)
		for _, e := range proxyEntries {
			pk := strings.SplitN(e.Address, ":", 2)[0]
			proxyByPK[pk] = e.Geo.Country
		}
	}

	if showMore && len(vpnData) > 0 {
		var vpnEntries []serviceEntry
		err := json.Unmarshal([]byte(vpnData), &vpnEntries)
		internal.Catch(cmdFlags, err)
		for _, e := range vpnEntries {
			pk := strings.SplitN(e.Address, ":", 2)[0]
			vpnByPK[pk] = e.Geo.Country
		}
	}

	if showMore && len(visorData) > 0 {
		var visorEntries []serviceEntry
		err := json.Unmarshal([]byte(visorData), &visorEntries)
		internal.Catch(cmdFlags, err)
		for _, e := range visorEntries {
			pk := strings.SplitN(e.Address, ":", 2)[0]
			visorByPK[pk] = e.Geo.Country
		}
	}

	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 5, ' ', tabwriter.TabIndent)

	// Print header
	if showMore && bwDays > 0 {
		_, err := fmt.Fprintln(w, "type\tid\tremote_pk\tmode\tlabel\tversion\tcountry\tservices\trecv\tsent")
		internal.Catch(cmdFlags, err)
	} else if showMore {
		_, err := fmt.Fprintln(w, "type\tid\tremote_pk\tmode\tlabel\tversion\tcountry\tservices")
		internal.Catch(cmdFlags, err)
	} else if bwDays > 0 {
		_, err := fmt.Fprintln(w, "type\tid\tremote_pk\tmode\tlabel\trecv\tsent")
		internal.Catch(cmdFlags, err)
	} else {
		_, err := fmt.Fprintln(w, "type\tid\tremote_pk\tmode\tlabel")
		internal.Catch(cmdFlags, err)
	}

	type outputTP struct {
		Type      types.Type      `json:"type"`
		ID        uuid.UUID       `json:"id"`
		Remote    cipher.PubKey   `json:"remote_pk"`
		TpMode    string          `json:"mode"`
		Label     transport.Label `json:"label"`
		Version   string          `json:"version,omitempty"`
		Country   string          `json:"country,omitempty"`
		Services  string          `json:"services,omitempty"`
		RecvBytes uint64          `json:"recv_bytes,omitempty"`
		SentBytes uint64          `json:"sent_bytes,omitempty"`
		Inactive  bool            `json:"inactive,omitempty"`
	}

	var outputTPS []outputTP

	// Track line numbers that are inactive (for red coloring)
	inactiveLines := make(map[int]bool)
	lineNum := 1 // Start at 1 (line 0 is header)

	// Add active transports
	for _, tp := range tps {
		if tp == nil {
			continue
		}
		tpMode := "regular"
		if tp.IsSetup {
			tpMode = "setup"
		}

		// Use bandwidth from aggregated logs if available, otherwise from current transport
		var recvBytes, sentBytes uint64
		if bw, ok := bwByTpID[tp.ID.String()]; ok && bwDays > 0 {
			recvBytes = bw.recv
			sentBytes = bw.sent
		} else if tp.Log != nil {
			if tp.Log.RecvBytes != nil {
				recvBytes = *tp.Log.RecvBytes
			}
			if tp.Log.SentBytes != nil {
				sentBytes = *tp.Log.SentBytes
			}
		}
		tp.Log = nil

		version := ""
		if showMore && versionsByPK != nil {
			version = versionsByPK[tp.Remote.String()]
		}

		var country, services string
		pk := tp.Remote.String()
		proxyCountry, inProxy := proxyByPK[pk]
		vpnCountry, inVPN := vpnByPK[pk]
		visorCountry, inVisor := visorByPK[pk]

		// Build services list
		var svcList []string
		var countries []string

		if inProxy {
			svcList = append(svcList, "proxy")
			countries = append(countries, proxyCountry)
		}
		if inVPN {
			svcList = append(svcList, "vpn")
			countries = append(countries, vpnCountry)
		}
		if inVisor {
			svcList = append(svcList, "visor")
			countries = append(countries, visorCountry)
		}

		if len(svcList) > 0 {
			services = strings.Join(svcList, ",")
			// Deduplicate countries
			countryMap := make(map[string]bool)
			var uniqueCountries []string
			for _, c := range countries {
				if c != "" && !countryMap[c] {
					countryMap[c] = true
					uniqueCountries = append(uniqueCountries, c)
				}
			}
			country = strings.Join(uniqueCountries, "/")
		}

		oTP := outputTP{
			Type:      tp.Type,
			ID:        tp.ID,
			Remote:    tp.Remote,
			TpMode:    tpMode,
			Label:     tp.Label,
			Version:   version,
			Country:   country,
			Services:  services,
			RecvBytes: recvBytes,
			SentBytes: sentBytes,
		}
		outputTPS = append(outputTPS, oTP)

		if showMore && bwDays > 0 {
			_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				tp.Type, tp.ID, tp.Remote, tpMode, tp.Label, version, country, services,
				formatBytes(recvBytes), formatBytes(sentBytes))
			internal.Catch(cmdFlags, err)
		} else if showMore {
			_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				tp.Type, tp.ID, tp.Remote, tpMode, tp.Label, version, country, services)
			internal.Catch(cmdFlags, err)
		} else if bwDays > 0 {
			_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				tp.Type, tp.ID, tp.Remote, tpMode, tp.Label,
				formatBytes(recvBytes), formatBytes(sentBytes))
			internal.Catch(cmdFlags, err)
		} else {
			_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				tp.Type, tp.ID, tp.Remote, tpMode, tp.Label)
			internal.Catch(cmdFlags, err)
		}
		lineNum++
	}

	// Add inactive transports to the same table (if any)
	for _, itp := range inactive {
		oTP := outputTP{
			ID:        itp.ID,
			TpMode:    "inactive",
			RecvBytes: itp.RecvBytes,
			SentBytes: itp.SentBytes,
			Inactive:  true,
		}
		outputTPS = append(outputTPS, oTP)

		if showMore && bwDays > 0 {
			_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				"-", itp.ID, "-", "inactive", "-", "-", "-", "-",
				formatBytes(itp.RecvBytes), formatBytes(itp.SentBytes))
			internal.Catch(cmdFlags, err)
		} else if showMore {
			_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				"-", itp.ID, "-", "inactive", "-", "-", "-", "-")
			internal.Catch(cmdFlags, err)
		} else if bwDays > 0 {
			_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				"-", itp.ID, "-", "inactive", "-",
				formatBytes(itp.RecvBytes), formatBytes(itp.SentBytes))
			internal.Catch(cmdFlags, err)
		} else {
			_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				"-", itp.ID, "-", "inactive", "-")
			internal.Catch(cmdFlags, err)
		}
		inactiveLines[lineNum] = true
		lineNum++
	}

	// Flush and format table with red coloring for inactive lines
	internal.Catch(cmdFlags, w.Flush())

	var tableOutput strings.Builder
	lines := strings.Split(b.String(), "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		if inactiveLines[i] {
			tableOutput.WriteString(pterm.Red(line))
		} else {
			tableOutput.WriteString(line)
		}
		tableOutput.WriteString("\n")
	}

	internal.PrintOutput(cmdFlags, outputTPS, tableOutput.String())
}
