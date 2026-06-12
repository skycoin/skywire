// Package clireward cmd/skywire-cli/commands/reward/lookup.go
package clireward

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
)

var (
	lookupDays     int
	lookupServer   string
	lookupFile     string
	lookupRewarded bool
)

func init() {
	RootCmd.AddCommand(lookupCmd)
	lookupCmd.Flags().IntVarP(&lookupDays, "days", "n", 7, "days to look back (1-90)")
	lookupCmd.Flags().StringVarP(&lookupServer, "server", "S", deployment.Prod.RewardSystemDmsg, "reward system dmsg address (dmsg://<pk>:<port>)")
	lookupCmd.Flags().StringVarP(&lookupFile, "file", "f", "", "file of public keys, one per line")
	lookupCmd.Flags().BoolVar(&lookupRewarded, "rewarded-only", false, "only show days with a nonzero reward")
}

// lookupDayReward mirrors one entry of the reward server's
// /skycoin-rewards/visor/:pk JSON history.
type lookupDayReward struct {
	Date   string  `json:"date"`
	Amount float64 `json:"amount"`
	Share  float64 `json:"share"`
	Sent   bool    `json:"sent"`
	Txid   string  `json:"txid,omitempty"`
}

// lookupVisorReward is the per-PK result (the server response plus a
// client-side Error for PKs whose request failed, so one bad key doesn't
// abort the batch).
type lookupVisorReward struct {
	PK      string            `json:"pk"`
	Days    int               `json:"days"`
	History []lookupDayReward `json:"history"`
	Error   string            `json:"error,omitempty"`
}

var lookupCmd = &cobra.Command{
	Use:   "lookup [pubkey...]",
	Short: "Look up reward history for public keys (over dmsg)",
	Long: `Query the reward system for the per-day reward history of one or more
public keys: which days each key was rewarded, the reward SKY amount, the
reward shares, whether the payout was sent, and the txid.

The request rides DMSG (no plain HTTP) to the reward system's dmsg address —
so it works from any visor without exposing the request to the clearnet.
Public keys come from arguments and/or --file (one per line; a line's first
whitespace field is taken, so a CSV/notes column is tolerated).

Unlike 'cli rewards -k', which recomputes from local uptime + survey data,
this reads the reward server's already-published results — no local data
needed.

Examples:
  skywire cli rewards lookup 0324c17a...f1
  skywire cli rewards lookup --file pklist.txt --days 5
  skywire cli rewards lookup --file pklist.txt --rewarded-only --json`,
	Run: func(cmd *cobra.Command, args []string) {
		pks, err := collectLookupPKs(args, lookupFile)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if len(pks) == 0 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("no public keys given (pass as args or with --file)"))
		}
		if lookupDays < 1 || lookupDays > 90 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--days must be between 1 and 90"))
		}
		base := strings.TrimRight(lookupServer, "/")
		if !strings.HasPrefix(base, "dmsg://") {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--server must be a dmsg:// address (got %q)", base))
		}

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		log := logging.MustGetLogger("rewards-lookup")

		pk, sk := cipher.GenerateKeyPair()
		dmsgC, stop, err := lookupDmsgClient(ctx, log, pk, sk)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("dmsg: %w", err))
		}
		defer stop()
		httpClient := &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}

		results := make([]lookupVisorReward, 0, len(pks))
		for _, k := range pks {
			results = append(results, fetchVisorReward(ctx, httpClient, base, k, lookupDays))
		}

		internal.PrintOutput(cmd.Flags(), results, renderLookupTable(results, lookupDays, lookupRewarded))
	},
}

// collectLookupPKs merges PKs from args and an optional file, validates each as
// a public key, and dedups while preserving order.
func collectLookupPKs(args []string, file string) ([]string, error) {
	seen := make(map[string]bool)
	var out []string
	add := func(s string) error {
		s = strings.TrimSpace(s)
		if s == "" || strings.HasPrefix(s, "#") {
			return nil
		}
		var pk cipher.PubKey
		if err := pk.Set(s); err != nil {
			return fmt.Errorf("invalid public key %q: %w", s, err)
		}
		h := pk.Hex()
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
		return nil
	}
	for _, a := range args {
		if err := add(a); err != nil {
			return nil, err
		}
	}
	if file != "" {
		f, err := os.Open(file) //nolint:gosec // operator-supplied path
		if err != nil {
			return nil, fmt.Errorf("open --file: %w", err)
		}
		defer f.Close() //nolint:errcheck
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) == 0 {
				continue
			}
			if err := add(fields[0]); err != nil {
				return nil, err
			}
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("read --file: %w", err)
		}
	}
	return out, nil
}

// fetchVisorReward GETs the reward history for one PK over dmsg. A request error
// is recorded in the result's Error field rather than aborting the batch.
func fetchVisorReward(ctx context.Context, c *http.Client, base, pk string, days int) lookupVisorReward {
	res := lookupVisorReward{PK: pk}
	url := fmt.Sprintf("%s/skycoin-rewards/visor/%s?days=%d", base, pk, days)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	resp, err := c.Do(req)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		res.Error = fmt.Sprintf("server returned %s", resp.Status)
		return res
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		res.Error = fmt.Sprintf("decode: %v", err)
		return res
	}
	if res.PK == "" {
		res.PK = pk
	}
	return res
}

// renderLookupTable formats the per-PK reward history as an aligned table.
func renderLookupTable(results []lookupVisorReward, days int, rewardedOnly bool) string {
	var buf strings.Builder
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PK\tDATE\tAMOUNT(SKY)\tSHARE\tSENT\tTXID") //nolint:errcheck
	for _, r := range results {
		short := r.PK
		if len(short) >= 8 {
			short = short[:8]
		}
		if r.Error != "" {
			fmt.Fprintf(tw, "%s\tERROR: %s\t-\t-\t-\t-\n", short, r.Error) //nolint:errcheck
			continue
		}
		any := false
		for _, d := range r.History {
			if rewardedOnly && d.Amount == 0 {
				continue
			}
			any = true
			sent := "no"
			if d.Sent {
				sent = "yes"
			}
			txid := d.Txid
			if txid == "" {
				txid = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\t%.6f\t%.6f\t%s\t%s\n", short, d.Date, d.Amount, d.Share, sent, txid) //nolint:errcheck
		}
		if !any {
			fmt.Fprintf(tw, "%s\t(no reward in %dd)\t-\t-\t-\t-\n", short, days) //nolint:errcheck
		}
	}
	tw.Flush() //nolint:errcheck,gosec
	return buf.String()
}

// lookupDmsgClient brings up an ephemeral dmsg client for the request. Mirrors
// the dmsg-curl bootstrap: dmsg discovery is reached over HTTP (inherent to
// dmsg bootstrap), but the reward request itself rides dmsg.
func lookupDmsgClient(ctx context.Context, log *logging.Logger, pk cipher.PubKey, sk cipher.SecKey) (*dmsg.Client, func(), error) {
	discURL := deployment.Prod.DmsgDiscovery
	if discURL == "" {
		discURL = "http://dmsgd.skywire.skycoin.com"
	}
	discClient := disc.NewHTTP(discURL, &http.Client{Timeout: 30 * time.Second}, log)
	dmsgConfig := dmsg.DefaultConfig()
	dmsgConfig.MinSessions = 1
	dmsgC := dmsg.NewClient(pk, sk, discClient, dmsgConfig)
	go dmsgC.Serve(ctx)

	select {
	case <-ctx.Done():
		_ = dmsgC.Close() //nolint:errcheck
		return nil, nil, ctx.Err()
	case <-dmsgC.Ready():
	case <-time.After(30 * time.Second):
		_ = dmsgC.Close() //nolint:errcheck
		return nil, nil, fmt.Errorf("timeout waiting for dmsg client")
	}
	return dmsgC, func() { _ = dmsgC.Close() }, nil //nolint:errcheck
}
