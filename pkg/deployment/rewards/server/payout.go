// Package rewardsserver pkg/deployment/rewards/server/payout.go c3-app-rewards
package rewardsserver

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
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

// errPayoutPending is returned while a day's <day>.txt.pending marker exists:
// a send started and did not finish, so the day is neither paid nor payable.
var errPayoutPending = errors.New("a reward send did not finish")

// errCycleRunning refuses a payout while the systemd reward cycle runs.
var errCycleRunning = errors.New("reward calculation running")

// noTxid reports whether the day's .txt holds no transaction id yet ("test"
// counts as none).
func noTxid(histDir, day string) bool {
	b, err := os.ReadFile(filepath.Join(histDir, day+".txt")) //nolint:gosec
	g := strings.TrimSpace(string(b))
	return err != nil || g == "" || g == "test"
}

// payoutPending reports whether a send of the day's rewards is in flight or
// did not finish. The calculation leaves such a day alone.
func payoutPending(histDir, day string) bool {
	_, err := os.Stat(filepath.Join(histDir, day+".txt.pending"))
	return err == nil
}

// undistributed reports whether the day's rewards have not been sent nor
// started to be.
func undistributed(histDir, day string) bool {
	return noTxid(histDir, day) && !payoutPending(histDir, day)
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
		if noTxid(histDir, d) && payoutPending(histDir, d) {
			return d, nil, fmt.Errorf("%w: %s.txt.pending exists. Check the chain for the transaction, then remove the file or write the transaction id to %s.txt", errPayoutPending, d, d)
		}
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

// payoutExclusive runs fn with no reward calculation running: under the
// scheduler's run lock, or, with the systemd timer, only while its unit is not
// active.
func payoutExclusive(fn func() error) error {
	if s := currentScheduler(); s != nil {
		return s.Exclusive(fn)
	}
	if cycleActive() == "active" {
		return errCycleRunning
	}
	return fn()
}

// payoutError is a refused or failed payout, with its HTTP status.
type payoutError struct {
	code int
	msg  string
	err  error
}

func (e *payoutError) Error() string { return e.msg }
func (e *payoutError) Unwrap() error { return e.err }

// sendPayout pays the oldest undistributed day with rawTxn. From reading the
// day's payout csv to recording the transaction id, no calculation runs, and
// the transaction must pay that csv exactly.
func sendPayout(histDir, rawTxn, csvHash string, broadcast func() (string, error)) (day, txid string, err error) {
	err = payoutExclusive(func() error {
		var csv []byte
		day, csv, err = nextPayout(histDir)
		switch {
		case errors.Is(err, errNoPayout):
			return &payoutError{http.StatusNotFound, "No undistributed rewards csv found.", err}
		case errors.Is(err, errPayoutPending):
			return &payoutError{http.StatusConflict, err.Error(), err}
		case err != nil:
			return &payoutError{http.StatusServiceUnavailable, "payout csv for " + day + ": " + err.Error(), err}
		}
		if csvHash == "" {
			return &payoutError{http.StatusPreconditionRequired, "send the " + PayoutCSVHeader + " header of GET /skycoin-rewards/payout with the transaction", errPayoutStale}
		}
		if csvHash != PayoutCSVHash(csv) {
			return &payoutError{http.StatusConflict, "rewards for " + day + " were recalculated since the payout csv was fetched; run sendrewards again", errPayoutStale}
		}
		// Never send to a single reward address that has sent coins.
		if err := checkPayoutTxn(histDir, day, rawTxn); err != nil {
			log.Warnf("reward transaction for %s refused: %v", day, err)
			return &payoutError{http.StatusBadRequest, "reward transaction for " + day + " refused:\n\n" + err.Error() + "\n\nBuild it from GET /skycoin-rewards/payout instead.", err}
		}
		want, err := rewards.PayoutDroplets(csv)
		if err != nil {
			return &payoutError{http.StatusInternalServerError, "payout csv for " + day + ": " + err.Error(), err}
		}
		full, err := os.ReadFile(filepath.Join(histDir, day+"_rewardtxn0.csv")) //nolint:gosec
		if err != nil {
			return &payoutError{http.StatusInternalServerError, "payout csv for " + day + ": " + err.Error(), err}
		}
		if err := rewards.CheckTransactionPaysExactly(rawTxn, want, rewards.CSVAddresses(full)); err != nil {
			log.Warnf("reward transaction for %s refused: %v", day, err)
			return &payoutError{http.StatusConflict, "reward transaction for " + day + " does not pay the payout csv exactly:\n\n" + err.Error(), err}
		}
		txid, err = broadcastAndRecord(filepath.Join(histDir, day+".txt"), broadcast)
		if errors.Is(err, errUnrecorded) {
			log.Errorf("reward transaction %s for %s was broadcast but not recorded: %v", strings.TrimSpace(txid), day, err)
			return &payoutError{http.StatusInternalServerError, "THE TRANSACTION WAS BROADCAST. Do not send it again.\n\nWrite this transaction id to " + filepath.Join(histDir, day+".txt") + " by hand:\n\n" + txid + "\n\nerror:\n\n" + err.Error(), err}
		}
		if err != nil {
			return &payoutError{http.StatusInternalServerError, "skywire skycoin cli broadcastTransaction:\n\n" + txid + "\n\nerror:\n\n" + err.Error(), err}
		}
		return nil
	})
	if errors.Is(err, errCycleRunning) {
		err = &payoutError{http.StatusServiceUnavailable, err.Error(), err}
	}
	return day, txid, err
}

// PayoutCSVHeader carries PayoutCSVHash of the csv GET /skycoin-rewards/payout
// served. POST /reward must send it back, so a csv recalculated in between is
// refused.
const PayoutCSVHeader = "X-Reward-CSV"

// errPayoutStale refuses a transaction built from another payout csv.
var errPayoutStale = errors.New("payout csv changed")

// PayoutCSVHash is the hex sha256 of a payout csv.
func PayoutCSVHash(csv []byte) string {
	h := sha256.Sum256(csv)
	return hex.EncodeToString(h[:])
}
