// Package clitp cmd/skywire-cli/commands/tp/tp_skynet.go c4-vis-cli
package clitp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// skynetListResult is one visor's own transport list, as --json prints it.
type skynetListResult struct {
	PK         cipher.PubKey      `json:"pk"`
	At         time.Time          `json:"at,omitzero"`
	Transports []*transport.Entry `json:"transports,omitempty"`
	Error      string             `json:"error,omitempty"`
}

// fetchSkynetList reads pk's signed transport list from its :80 over a
// skywire transport, through the local visor, and checks the signature.
func fetchSkynetList(rpcClient visorapi.API, pk cipher.PubKey) (*transport.SignedList, error) {
	resp, err := rpcClient.SkynetHTTP(visorapi.SkynetHTTPRequest{
		PK:     pk,
		Port:   visorconfig.DmsgHTTPPort,
		Path:   "/transports",
		Method: http.MethodGet,
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(resp.Body))
	}
	var l transport.SignedList
	if err := json.Unmarshal(resp.Body, &l); err != nil {
		return nil, err
	}
	if l.PK != pk {
		return nil, fmt.Errorf("the list is %s's, not %s's", l.PK, pk)
	}
	if err := l.Verify(); err != nil {
		return nil, fmt.Errorf("signature: %w", err)
	}
	return &l, nil
}

// listSkynetTransports prints each visor's own signed transport list,
// filtered by --types and --pks.
func listSkynetTransports(cmd *cobra.Command, rpcClient visorapi.API, pkStrs []string) {
	isJSON, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
	var pks []cipher.PubKey
	for _, s := range pkStrs {
		var pk cipher.PubKey
		if err := pk.Set(s); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid public key %q: %w", s, err))
		}
		pks = append(pks, pk)
	}
	var all []skynetListResult
	for i, pk := range pks {
		res := skynetListResult{PK: pk}
		l, err := fetchSkynetList(rpcClient, pk)
		if err != nil {
			res.Error = err.Error()
		} else {
			res.At = time.Unix(l.At, 0).UTC()
			for _, e := range l.Transports() {
				if len(filterTypes) > 0 && !slices.Contains(filterTypes, string(e.Type)) {
					continue
				}
				if len(filterPubKeys) > 0 && !slices.Contains(filterPubKeys, e.RemoteEdge(pk).Hex()) {
					continue
				}
				res.Transports = append(res.Transports, e)
			}
		}
		all = append(all, res)
		if isJSON {
			continue
		}
		if res.Error != "" {
			fmt.Printf("[%d/%d] %s (error: %s)\n", i+1, len(pks), pk, res.Error)
			continue
		}
		fmt.Printf("[%d/%d] %s (%d transports, signed %s)\n", i+1, len(pks), pk, len(res.Transports), res.At.Format(time.RFC3339))
		if len(res.Transports) == 0 {
			continue
		}
		var b bytes.Buffer
		w := tabwriter.NewWriter(&b, 0, 0, 3, ' ', tabwriter.TabIndent)
		fmt.Fprintln(w, "  type\tid\tremote\tlabel") //nolint:errcheck
		for _, e := range res.Transports {
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", e.Type, e.ID, e.RemoteEdge(pk), e.Label) //nolint:errcheck
		}
		w.Flush() //nolint:errcheck,gosec
		fmt.Print(b.String())
	}
	if isJSON {
		internal.PrintOutput(cmd.Flags(), all, "")
	}
}
