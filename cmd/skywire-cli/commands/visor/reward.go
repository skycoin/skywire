// Package clivisor cmd/skywire-cli/commands/visor/reward.go
package clivisor

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/visor"
)

func init() {
	rewardCmd.Flags().IntVarP(&rewardDays, "days", "d", 7, "number of days of history")
	rewardCmd.Flags().StringVarP(&rewardPK, "pk", "k", "", "visor public key (default: local visor)")
	rewardCmd.Flags().BoolVarP(&rewardAll, "all", "a", false, "show rewards for all visors connected to the hypervisor")
	rewardCmd.Flags().BoolVarP(&rewardJSON, "json", "j", false, "output as JSON")
	RootCmd.AddCommand(rewardCmd)
}

var (
	rewardDays int
	rewardPK   string
	rewardAll  bool
	rewardJSON bool
)

var rewardCmd = &cobra.Command{
	Use:   "reward",
	Short: "Show reward history for a visor",
	Long:  "Fetches reward history from the reward system via the visor's DMSG connection.",
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			logger.Fatal("RPC connection failed: ", err)
		}

		rewardDmsg := deployment.Prod.RewardSystemDmsg
		if rewardDmsg == "" {
			logger.Fatal("No reward system DMSG address configured")
		}

		// Collect PKs to query
		var pks []string
		if rewardAll {
			// Include the local visor
			overview, err := rpcClient.Overview()
			if err == nil {
				pks = append(pks, overview.PubKey.Hex())
			}
			// Get all connected remote visors
			remoteVisors, err := rpcClient.RemoteVisors()
			if err == nil {
				pks = append(pks, remoteVisors...)
			}
			if len(pks) == 0 {
				logger.Fatal("No visors found")
			}
		} else if rewardPK != "" {
			pks = []string{rewardPK}
		} else {
			overview, err := rpcClient.Overview()
			if err != nil {
				logger.Fatal("Failed to get visor overview: ", err)
			}
			pks = []string{overview.PubKey.Hex()}
		}

		for i, pk := range pks {
			if i > 0 {
				fmt.Println()
			}
			fetchAndDisplayReward(rpcClient, rewardDmsg, pk)
		}
	},
}

func fetchAndDisplayReward(rpcClient visor.API, rewardDmsg, pk string) {
	url := fmt.Sprintf("%s/skycoin-rewards/visor/%s?days=%d", rewardDmsg, pk, rewardDays)

	resp, err := rpcClient.DmsgHTTP(visor.DmsgHTTPRequest{
		URL:    url,
		Method: "GET",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to fetch reward data for %s: %v\n", pk[:16]+"...", err)
		return
	}
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "Reward system returned status %d for %s\n", resp.StatusCode, pk[:16]+"...")
		return
	}

	if rewardJSON {
		fmt.Println(string(resp.Body))
		return
	}

	var result struct {
		PK      string `json:"pk"`
		Days    int    `json:"days"`
		History []struct {
			Date   string  `json:"date"`
			Amount float64 `json:"amount"`
			Share  float64 `json:"share"`
			Sent   bool    `json:"sent"`
			Txid   string  `json:"txid,omitempty"`
		} `json:"history"`
	}

	if err := json.Unmarshal(resp.Body, &result); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse reward data for %s: %v\n", pk[:16]+"...", err)
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "Reward history for %s (%d days)\n\n", pk[:16]+"...", rewardDays) //nolint:errcheck,gosec
	fmt.Fprintf(w, "DATE\tAMOUNT (SKY)\tSHARE (%%)\tSTATUS\tTXID\n")                 //nolint:errcheck,gosec
	fmt.Fprintf(w, "----\t------------\t---------\t------\t----\n")                  //nolint:errcheck,gosec

	var total float64
	for _, day := range result.History {
		status := "-"
		if day.Amount > 0 {
			if day.Sent {
				status = "Sent"
			} else {
				status = "Pending"
			}
		}
		txid := "-"
		if day.Txid != "" && len(day.Txid) > 12 {
			txid = day.Txid[:12] + "..."
		} else if day.Txid != "" {
			txid = day.Txid
		}
		amountStr := "-"
		shareStr := "-"
		if day.Amount > 0 {
			amountStr = fmt.Sprintf("%.6f", day.Amount)
			shareStr = fmt.Sprintf("%.4f", day.Share)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", day.Date, amountStr, shareStr, status, txid) //nolint:errcheck,gosec
		total += day.Amount
	}

	fmt.Fprintf(w, "\nTotal:\t%.6f SKY\n", total) //nolint:errcheck,gosec
	w.Flush()                                     //nolint:errcheck,gosec
}
