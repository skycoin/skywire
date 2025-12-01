// Package clitp cmd/skywire-cli/commands/tp/tp-disc.go
package clitp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/tpdclient"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

var (
	tpID      string
	tpPK      string
	tpdDirect bool
)

func init() {
	discTpCmd.Flags().StringVarP(&tpID, "id", "i", "", "obtain transport of given ID")
	discTpCmd.Flags().StringVarP(&tpPK, "pk", "p", "", "obtain transports by public key")
	discTpCmd.Flags().StringVar(&tpdURL, "tpdurl", "", "transport discovery url (e.g., http://transport-discovery:9091)")
	discTpCmd.Flags().BoolVar(&tpdDirect, "direct", false, "query transport discovery directly, bypass RPC")
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

		// Determine if we should query transport discovery directly
		useDirectQuery := tpdDirect || tpdURL != ""

		// Try RPC first unless direct query is requested
		if !useDirectQuery {
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err == nil {
				// RPC available, use it
				if tppk.Null() {
					entry, err := rpcClient.DiscoverTransportByID(uuid.UUID(tpid))
					if err == nil {
						PrintTransportEntries(cmd.Flags(), entry)
						return
					}
					// RPC query failed, fall back to direct query
					fmt.Fprintf(os.Stderr, "RPC query failed: %v, falling back to direct query...\n", err)
					useDirectQuery = true
				} else {
					entries, err := rpcClient.DiscoverTransportsByPK(tppk)
					if err == nil {
						PrintTransportEntries(cmd.Flags(), entries...)
						return
					}
					// RPC query failed, fall back to direct query
					fmt.Fprintf(os.Stderr, "RPC query failed: %v, falling back to direct query...\n", err)
					useDirectQuery = true
				}
			} else {
				// RPC connection failed, fall back to direct query
				fmt.Fprintf(os.Stderr, "RPC connection failed: %v, falling back to direct query...\n", err)
				useDirectQuery = true
			}
		}

		// Query transport discovery directly
		if useDirectQuery {
			// Use provided URL or default
			url := tpdURL
			if url == "" {
				url = deployment.Prod.TransportDiscovery
			}

			// Create HTTP client for transport discovery
			masterLogger := logging.NewMasterLogger()
			tpdClient, err := tpdclient.NewHTTP(url, cipher.PubKey{}, cipher.SecKey{}, &http.Client{}, "", masterLogger)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("failed to create transport discovery client: %w", err))
			}

			ctx := context.Background()
			if tppk.Null() {
				entry, err := tpdClient.GetTransportByID(ctx, uuid.UUID(tpid))
				internal.Catch(cmd.Flags(), err)
				PrintTransportEntries(cmd.Flags(), entry)
			} else {
				entries, err := tpdClient.GetTransportsByEdge(ctx, tppk)
				internal.Catch(cmd.Flags(), err)
				PrintTransportEntries(cmd.Flags(), entries...)
			}
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
