// Package clitp cmd/skywire-cli/commands/tp/tp-disc.go
package clitp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

var (
	tpID    string
	tpPK    string
	tpdHTTP bool
)

func init() {
	discTpCmd.Flags().StringVarP(&tpID, "id", "i", "", "obtain transport of given ID")
	discTpCmd.Flags().StringVarP(&tpPK, "pk", "p", "", "obtain transports by public key")
	discTpCmd.Flags().StringVar(&tpdURL, "tpdurl", deployment.Prod.TransportDiscovery, "transport discovery url")
	discTpCmd.Flags().BoolVar(&tpdHTTP, "http", false, "query transport discovery via HTTP, bypass RPC")
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

		// Determine if we should query transport discovery via HTTP
		useHTTPQuery := tpdHTTP

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

// getTransportByID queries transport discovery for a transport by ID using FetchServiceURL
func getTransportByID(cmdFlags *pflag.FlagSet, baseURL string, id uuid.UUID) (*transport.Entry, error) {
	url := fmt.Sprintf("%s/all-transports", baseURL)
	body, err := clirpc.FetchServiceURL(cmdFlags, url)
	if err != nil {
		return nil, fmt.Errorf("failed to query transport discovery: %w", err)
	}

	var entries []*transport.Entry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
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
	url := fmt.Sprintf("%s/all-transports", baseURL)
	body, err := clirpc.FetchServiceURL(cmdFlags, url)
	if err != nil {
		return nil, fmt.Errorf("failed to query transport discovery: %w", err)
	}

	var allEntries []*transport.Entry
	if err := json.Unmarshal(body, &allEntries); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
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

	type outputEntry struct {
		ID    uuid.UUID     `json:"id"`
		Type  types.Type    `json:"type"`
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
