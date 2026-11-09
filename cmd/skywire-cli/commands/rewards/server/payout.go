package clirewardsserver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/skycoin/skywire/pkg/deployment/rewards"
)

// errNoPayout is returned when every day's rewards were already sent.
var errNoPayout = errors.New("no undistributed rewards csv found")

// payoutAddressSpent asks the skycoin node whether a reward address has sent
// coins. A variable so tests can stand in for the node.
var payoutAddressSpent = func(addr string) (bool, error) {
	node := skycoinNode
	if node == "" {
		node = "http://127.0.0.1:6420"
	}
	return rewards.NodeAddressSpent(node)(addr)
}

// undistributed reports whether the day's rewards have not been sent: its
// .txt holds no transaction id yet ("test" counts as none).
func undistributed(histDir, day string) bool {
	b, err := os.ReadFile(filepath.Join(histDir, day+".txt")) //nolint:gosec
	g := strings.TrimSpace(string(b))
	return err != nil || g == "" || g == "test"
}

// nextPayout is the oldest day not yet distributed, with its reward
// transaction CSV less the payouts to single addresses that have sent coins.
// Those are written to <day>_skipped.txt in histDir and logged. Nothing is
// returned when the node cannot be asked.
func nextPayout(histDir string) (day string, csv []byte, err error) {
	dates, err := histRewardDates(histDir)
	if err != nil {
		return "", nil, err
	}
	for _, d := range dates {
		if !undistributed(histDir, d) {
			continue
		}
		txn, err := os.ReadFile(filepath.Join(histDir, d+"_rewardtxn0.csv")) //nolint:gosec
		if err != nil {
			return d, nil, err
		}
		shares, err := os.ReadFile(filepath.Join(histDir, d+"_shares.csv")) //nolint:gosec
		if err != nil {
			return d, nil, err
		}
		out, skipped, err := rewards.FilterSpentPayouts(d, txn, shares, payoutAddressSpent)
		if err != nil {
			return d, nil, err
		}
		if len(skipped) > 0 {
			var b strings.Builder
			for _, s := range skipped {
				log.Warn(s.String())
				b.WriteString(s.String() + "\n")
			}
			if err := os.WriteFile(filepath.Join(histDir, d+"_skipped.txt"), []byte(b.String()), 0644); err != nil { //nolint:gosec
				return d, nil, err
			}
		}
		return d, out, nil
	}
	return "", nil, errNoPayout
}

// checkPayoutTxn refuses a raw transaction for day that pays a single reward
// address that has sent coins, or when the node cannot be asked.
func checkPayoutTxn(histDir, day, rawTxn string) error {
	shares, err := os.ReadFile(filepath.Join(histDir, day+"_shares.csv")) //nolint:gosec
	if err != nil {
		return err
	}
	return rewards.CheckTransactionPayouts(day, rawTxn, shares, payoutAddressSpent)
}
