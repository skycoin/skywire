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
	rewardCmd.Flags().BoolVarP(&rewardJSON, "json", "j", false, "output as JSON")
	RootCmd.AddCommand(rewardCmd)
}

var (
	rewardDays int
	rewardPK   string
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

		// If no PK specified, use the local visor's PK
		if rewardPK == "" {
			overview, err := rpcClient.Overview()
			if err != nil {
				logger.Fatal("Failed to get visor overview: ", err)
			}
			rewardPK = overview.PubKey.Hex()
		}

		// Build the reward system URL
		rewardDmsg := deployment.Prod.RewardSystemDmsg
		if rewardDmsg == "" {
			logger.Fatal("No reward system DMSG address configured")
		}

		url := fmt.Sprintf("%s/skycoin-rewards/visor/%s?days=%d", rewardDmsg, rewardPK, rewardDays)

		resp, err := rpcClient.DmsgHTTP(visor.DmsgHTTPRequest{
			URL:    url,
			Method: "GET",
		})
		if err != nil {
			logger.Fatal("Failed to fetch reward data: ", err)
		}
		if resp.StatusCode != 200 {
			logger.Fatalf("Reward system returned status %d: %s", resp.StatusCode, string(resp.Body))
		}

		if rewardJSON {
			fmt.Println(string(resp.Body))
			return
		}

		// Parse and display
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
			logger.Fatal("Failed to parse reward data: ", err)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "Reward history for %s (%d days)\n\n", rewardPK[:16]+"...", rewardDays)
		fmt.Fprintf(w, "DATE\tAMOUNT (SKY)\tSHARE (%%)\tSTATUS\tTXID\n")
		fmt.Fprintf(w, "----\t------------\t---------\t------\t----\n")

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
			if day.Txid != "" {
				txid = day.Txid[:12] + "..."
			}
			amountStr := "-"
			shareStr := "-"
			if day.Amount > 0 {
				amountStr = fmt.Sprintf("%.6f", day.Amount)
				shareStr = fmt.Sprintf("%.4f", day.Share)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", day.Date, amountStr, shareStr, status, txid)
			total += day.Amount
		}

		fmt.Fprintf(w, "\nTotal:\t%.6f SKY\n", total)
		w.Flush()
	},
}
