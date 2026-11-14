// Package rewardsserver pkg/deployment/rewards/server/payout.go c3-app-rewards
package rewardsserver

import (
	"errors"
	"fmt"
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

// errUnrecorded marks a transaction that was broadcast but whose id could not
// be written to the day's marker file.
var errUnrecorded = errors.New("transaction broadcast but not recorded")

// broadcastAndRecord reserves the day's marker file, broadcasts, then records
// the transaction id. A marker that cannot be reserved stops the broadcast.
func broadcastAndRecord(marker string, broadcast func() (string, error)) (string, error) {
	pending := marker + ".pending"
	f, err := os.OpenFile(pending, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec
	if errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("%s exists: an earlier send did not finish. Check the chain for it, then remove the file or write the transaction id to %s", pending, marker)
	}
	if err != nil {
		return "", fmt.Errorf("reserve the marker file: %w", err)
	}
	txid, err := broadcast()
	if err != nil {
		_ = f.Close()          //nolint:errcheck
		_ = os.Remove(pending) //nolint:errcheck
		return txid, err
	}
	if _, err = f.WriteString(txid); err == nil {
		err = f.Close()
	}
	if err == nil {
		err = os.Rename(pending, marker)
	}
	if err != nil {
		return txid, fmt.Errorf("%w: %v", errUnrecorded, err)
	}
	return txid, nil
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
