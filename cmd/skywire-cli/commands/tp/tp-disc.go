// Package clitp cmd/skywire-cli/commands/tp/tp-disc.go c4-vis-cli
package clitp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cliout/clitp"
	"github.com/skycoin/skywire/pkg/transport"
)

var (
	tpID      string
	tpPK      string
	tpdHTTP   bool
	discStats bool
	discType  string
)

// pkLocalSentinel is the NoOptDefVal for --pk: a bare `-p` (the flag present
// with no value) resolves to the LOCAL visor's public key via the visor RPC.
// It is a placeholder no real (hex) public key can collide with.
const pkLocalSentinel = "local"

func init() {
	discTpCmd.Flags().StringVarP(&tpID, "id", "i", "", "obtain transport of given ID")
	discTpCmd.Flags().StringVarP(&tpPK, "pk", "p", "", "obtain transports by public key (bare -p = the local visor pk)")
	// A bare `-p` (present, no value) resolves to the local visor pk.
	if f := discTpCmd.Flags().Lookup("pk"); f != nil {
		f.NoOptDefVal = pkLocalSentinel
	}
	discTpCmd.Flags().BoolVarP(&discStats, "stats", "s", false, "transport summary (count by type, total); network-wide, or for one visor with --pk")
	discTpCmd.Flags().StringVarP(&discType, "type", "t", "", "list the public keys involved in transports of the given type (e.g. stcpr, sudph, dmsg)")
	discTpCmd.Flags().StringVar(&tpdURL, "tpdurl", deployment.Prod.TransportDiscovery, "transport discovery url")
	discTpCmd.Flags().BoolVar(&tpdHTTP, "http", false, "skip the structured visor RPC and query transport discovery via the fetch chain (CXO→DmsgHTTP→DMSG)")
	// Wire the common fetch-path flags (--no-cxo/--no-rpc/--no-dmsg) so this
	// command honors them like every other deployment-service fetch.
	// FetchServiceURL (used in the fallback below) reads them.
	clirpc.RegisterFetchFlags(discTpCmd)
}

var discTpCmd = &cobra.Command{
	Use:   "disc",
	Short: "Discover remote transport(s)",
	Long: `
    Discover remote transport(s) by ID or public key.

    --stats/-s              network-wide transport summary (total, by type, unique visors)
    --stats --pk <pk>       the same summary computed over ONE visor's transports
    --stats -p              (bare -p) same, for the LOCAL visor's pk
    --type/-t <type>        list the public keys involved in transports of a type
    --type <type> --pk <pk> that visor's peers on the given transport type

Examples:
  skywire cli tp disc --id <transport-id>
  skywire cli tp disc --pk <public-key>
  skywire cli tp disc -s
  skywire cli tp disc -sp <public-key>
  skywire cli tp disc -sp
  skywire cli tp disc --type webrtc
  skywire cli tp disc --type stcpr --pk <public-key>`,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		// A bare `-p` (or `--pk` with an empty value) means "the local visor
		// pk" — resolve the sentinel to the real hex pk once here so every
		// path below (discovery, per-key stats, keys-by-type) sees a real key.
		resolveLocalPKSentinel(cmd)

		// --type: list the unique public keys involved in transports of a
		// given type. Standalone it is network-wide; with --pk it is that
		// visor's peers on that type. Independent of --id.
		if discType != "" {
			printKeysByType(cmd, tpdURL, discType, tpPK)
			return
		}

		// --stats: transport summary from Transport Discovery. Without --pk
		// this is the network-wide aggregate (computed server-side via
		// GET /all-transports/stats when available). With --pk it is the same
		// shape of summary computed over just that visor's transports.
		if discStats {
			if tpPK == "" {
				printNetworkTransportSummary(cmd, tpdURL)
			} else {
				printVisorTransportSummary(cmd, tpdURL, tpPK)
			}
			return
		}
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

		// Skip the structured visor RPC when --http is set, or when --no-rpc
		// disables the visor RPC step entirely (consistent with the common
		// fetch chain). In both cases we fall through to FetchServiceURL, which
		// itself honors --no-cxo/--no-rpc/--no-dmsg.
		useHTTPQuery := tpdHTTP || clirpc.NoRPC

		// Try RPC first unless HTTP query is requested
		if !useHTTPQuery {
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err == nil {
				// RPC available, use it
				if tppk.Null() {
					entry, err := rpcClient.DiscoverTransportByID(uuid.UUID(tpid))
					if err == nil {
						PrintTransportEntries(cmd.Flags(), entry)
						return
					}
					// RPC query failed, fall back to HTTP query
					fmt.Fprintf(os.Stderr, "RPC query failed: %v, falling back to HTTP query...\n", err)
					useHTTPQuery = true
				} else {
					entries, err := rpcClient.DiscoverTransportsByPK(tppk)
					if err == nil {
						PrintTransportEntries(cmd.Flags(), entries...)
						return
					}
					// RPC query failed, fall back to HTTP query
					fmt.Fprintf(os.Stderr, "RPC query failed: %v, falling back to HTTP query...\n", err)
					useHTTPQuery = true
				}
			} else {
				// RPC connection failed, fall back to HTTP query
				fmt.Fprintf(os.Stderr, "RPC connection failed: %v, falling back to HTTP query...\n", err)
				useHTTPQuery = true
			}
		}

		// Query transport discovery via HTTP
		if useHTTPQuery {
			// Query via FetchServiceURL (tries DmsgHTTP first, then plain HTTP)
			if tppk.Null() {
				entry, err := getTransportByID(cmd.Flags(), tpdURL, uuid.UUID(tpid))
				internal.Catch(cmd.Flags(), err)
				PrintTransportEntries(cmd.Flags(), entry)
			} else {
				entries, err := getTransportsByEdge(cmd.Flags(), tpdURL, tppk)
				internal.Catch(cmd.Flags(), err)
				PrintTransportEntries(cmd.Flags(), entries...)
			}
		}
	},
}

// resolveLocalPKSentinel turns a bare `-p` (tpPK == pkLocalSentinel) into the
// local visor's hex public key via the visor RPC. It is a no-op when --pk was
// given an explicit value or omitted entirely. Resolving the local pk requires
// the visor RPC (there is no other source of "this visor's" key), so a bare
// `-p` combined with --no-rpc is a fatal error.
func resolveLocalPKSentinel(cmd *cobra.Command) {
	if tpPK != pkLocalSentinel {
		return
	}
	rpcClient, err := clirpc.Client(cmd.Flags())
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("cannot resolve local visor pk (visor RPC unavailable): %w", err))
		return
	}
	overview, err := rpcClient.Overview()
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("cannot resolve local visor pk: %w", err))
		return
	}
	tpPK = overview.PubKey.Hex()
}

// fetchAllTransportEntries fetches and decodes the full /all-transports list
// from Transport Discovery via FetchServiceURL (DmsgHTTP → plain HTTP, honoring
// --no-cxo/--no-rpc/--no-dmsg). It is the shared fetch behind the by-ID,
// by-edge, per-key-stats and keys-by-type paths.
func fetchAllTransportEntries(cmdFlags *pflag.FlagSet, baseURL string) ([]*transport.Entry, error) {
	url := fmt.Sprintf("%s/all-transports", baseURL)
	body, err := clirpc.FetchServiceURL(cmdFlags, url)
	if err != nil {
		return nil, fmt.Errorf("failed to query transport discovery: %w", err)
	}

	var entries []*transport.Entry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return entries, nil
}

// getTransportByID queries transport discovery for a transport by ID using FetchServiceURL
func getTransportByID(cmdFlags *pflag.FlagSet, baseURL string, id uuid.UUID) (*transport.Entry, error) {
	entries, err := fetchAllTransportEntries(cmdFlags, baseURL)
	if err != nil {
		return nil, err
	}

	// Find the transport by ID
	for _, entry := range entries {
		if entry.ID == id {
			return entry, nil
		}
	}

	return nil, fmt.Errorf("transport not found")
}

// getTransportsByEdge queries transport discovery for transports by edge public key using FetchServiceURL
func getTransportsByEdge(cmdFlags *pflag.FlagSet, baseURL string, pk cipher.PubKey) ([]*transport.Entry, error) {
	allEntries, err := fetchAllTransportEntries(cmdFlags, baseURL)
	if err != nil {
		return nil, err
	}

	// Filter transports by edge
	var entries []*transport.Entry
	for _, entry := range allEntries {
		if entry.Edges[0] == pk || entry.Edges[1] == pk {
			entries = append(entries, entry)
		}
	}

	return entries, nil
}

// PrintTransportEntries prints the transport entries
func PrintTransportEntries(cmdFlags *pflag.FlagSet, entries ...*transport.Entry) {

	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 5, ' ', tabwriter.TabIndent)
	_, err := fmt.Fprintln(w, "id\ttype\tedge1\tedge2")
	internal.Catch(cmdFlags, err)

	type outputEntry = clitp.DiscEntry

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

// visorNetStatsOutput is the JSON/text payload for `tp disc -s --pk <pk>` (the
// per-visor transport summary from Transport Discovery). It mirrors
// netStatsOutput's shape but names the visor and reports its unique peer count.
type visorNetStatsOutput struct {
	PK           string         `json:"pk"`
	Total        int            `json:"total_transports"`
	ByType       map[string]int `json:"by_type"`
	UniqueVisors int            `json:"unique_visors"`
}

// aggregateVisorTransports counts a single visor's transports by type and
// collects the set of distinct peer visors (the other edge of each transport).
// entries are expected to already be that visor's transports (both edges), as
// returned by getTransportsByEdge.
func aggregateVisorTransports(entries []*transport.Entry, pk cipher.PubKey) (byType map[string]int, peers map[cipher.PubKey]struct{}) {
	byType = make(map[string]int)
	peers = make(map[cipher.PubKey]struct{})
	for _, e := range entries {
		byType[string(e.Type)]++
		peer := e.Edges[0]
		if peer == pk {
			peer = e.Edges[1]
		}
		peers[peer] = struct{}{}
	}
	return byType, peers
}

// printVisorTransportSummary fetches one visor's transports from Transport
// Discovery (reusing the by-edge fetch) and prints the same shape of summary as
// the network-wide `tp disc -s`, but computed only over that visor's
// transports. It is the shared implementation behind `tp disc -s --pk <pk>`.
func printVisorTransportSummary(cmd *cobra.Command, baseURL, pkStr string) {
	var pk cipher.PubKey
	internal.Catch(cmd.Flags(), pk.Set(pkStr))

	entries, err := getTransportsByEdge(cmd.Flags(), baseURL, pk)
	internal.Catch(cmd.Flags(), err)

	byType, peers := aggregateVisorTransports(entries, pk)
	vs := visorNetStatsOutput{
		PK:           pk.Hex(),
		Total:        len(entries),
		ByType:       byType,
		UniqueVisors: len(peers),
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Visor transports:  %d  (%s)\n", vs.Total, vs.PK)
	fmt.Fprintf(&b, "Unique peers:      %d\n\n", vs.UniqueVisors)
	fmt.Fprintf(&b, "  %-10s %s\n", "type", "total")
	typeNames := make([]string, 0, len(byType))
	for t := range byType {
		typeNames = append(typeNames, t)
	}
	sort.Strings(typeNames)
	for _, t := range typeNames {
		fmt.Fprintf(&b, "  %-10s %d\n", t, byType[t])
	}
	fmt.Fprintf(&b, "  %-10s %d\n", "total", vs.Total)

	internal.PrintOutput(cmd.Flags(), vs, b.String())
}

// keysByTypeOutput is the JSON/text payload for `tp disc --type <type>`: the
// distinct public keys involved in transports of that type. When scoped to a
// single visor (--pk), PK names the visor and PKs are its peers on that type.
type keysByTypeOutput struct {
	Type  string   `json:"type"`
	PK    string   `json:"pk,omitempty"`
	Count int      `json:"count"`
	PKs   []string `json:"pks"`
}

// collectKeysByType returns the sorted, de-duplicated hex public keys involved
// in transports of tpType. When hasFilter is set, only transports touching
// filter are considered and only the PEER edge (the other end) is collected —
// i.e. filter's peers on that transport type.
func collectKeysByType(entries []*transport.Entry, tpType string, filter cipher.PubKey, hasFilter bool) []string {
	set := make(map[cipher.PubKey]struct{})
	for _, e := range entries {
		if string(e.Type) != tpType {
			continue
		}
		if hasFilter {
			switch filter {
			case e.Edges[0]:
				set[e.Edges[1]] = struct{}{}
			case e.Edges[1]:
				set[e.Edges[0]] = struct{}{}
			}
			continue
		}
		set[e.Edges[0]] = struct{}{}
		set[e.Edges[1]] = struct{}{}
	}
	pks := make([]string, 0, len(set))
	for pk := range set {
		pks = append(pks, pk.Hex())
	}
	sort.Strings(pks)
	return pks
}

// printKeysByType fetches /all-transports, filters to transports of the given
// type, and prints the distinct public keys involved — network-wide, or (when
// pkFilter is set) the peers of that visor on the given type.
func printKeysByType(cmd *cobra.Command, baseURL, tpType, pkFilter string) {
	entries, err := fetchAllTransportEntries(cmd.Flags(), baseURL)
	internal.Catch(cmd.Flags(), err)

	var filter cipher.PubKey
	hasFilter := pkFilter != ""
	if hasFilter {
		internal.Catch(cmd.Flags(), filter.Set(pkFilter))
	}

	pks := collectKeysByType(entries, tpType, filter, hasFilter)

	out := keysByTypeOutput{
		Type:  tpType,
		Count: len(pks),
		PKs:   pks,
	}
	if hasFilter {
		out.PK = filter.Hex()
	}

	var b strings.Builder
	if hasFilter {
		fmt.Fprintf(&b, "%d %s peer(s) of %s:\n", out.Count, tpType, out.PK)
	} else {
		fmt.Fprintf(&b, "%d public key(s) on %s transports:\n", out.Count, tpType)
	}
	for _, pk := range pks {
		fmt.Fprintf(&b, "%s\n", pk)
	}

	internal.PrintOutput(cmd.Flags(), out, b.String())
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
