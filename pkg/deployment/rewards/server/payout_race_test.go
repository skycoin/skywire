package rewardsserver

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/coin"

	"github.com/skycoin/skywire/pkg/deployment/rewards"
)

const (
	raceDay   = "2026-11-10" // before the single address cutoff, so no node is asked
	racePayA  = "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6"
	racePayB  = "zU361Dk1sFqBMcTdMQbByfkJNzJyVPcT9h"
	raceShare = "Skycoin Address, Skywire Public Key, Reward Shares, Reward SKY Amount, IP, Architecture, UUID, Interfaces, Country, XPub\n"
)

// raceHist is a hist dir whose unpaid day pays a and b SKY.
func raceHist(t *testing.T, a, b string) string {
	t.Helper()
	dir := t.TempDir()
	writeRaceCSV(t, dir, a, b)
	if err := os.WriteFile(filepath.Join(dir, raceDay+"_shares.csv"), []byte(raceShare), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeRaceCSV rewrites the day's reward transaction csv, as a Calc does.
func writeRaceCSV(t *testing.T, dir, a, b string) {
	t.Helper()
	csv := "Skycoin Address, Reward Amount\n" + racePayA + ", " + a + "\n" + racePayB + ", " + b + "\n"
	if err := os.WriteFile(filepath.Join(dir, raceDay+"_rewardtxn0.csv"), []byte(csv), 0o600); err != nil {
		t.Fatal(err)
	}
}

// raceTxn is the transaction sendrewards.sh builds from the payout csv of
// dir as it is now, with change back to the sending address.
func raceTxn(t *testing.T, dir string) string {
	t.Helper()
	_, csv, err := nextPayout(dir)
	if err != nil {
		t.Fatal(err)
	}
	want, err := rewards.PayoutDroplets(csv)
	if err != nil {
		t.Fatal(err)
	}
	pk, sk, err := cipher.GenerateDeterministicKeyPair([]byte("reward wallet"))
	if err != nil {
		t.Fatal(err)
	}
	var txn coin.Transaction
	txn.In = []cipher.SHA256{cipher.SumSHA256([]byte("uxout"))}
	for a, c := range want {
		txn.Out = append(txn.Out, coin.TransactionOutput{Address: a, Coins: c, Hours: 1})
	}
	txn.Out = append(txn.Out, coin.TransactionOutput{Address: cipher.AddressFromPubKey(pk), Coins: 100e6, Hours: 1})
	if err := txn.SignInputs([]cipher.SecKey{sk}); err != nil {
		t.Fatal(err)
	}
	if err := txn.UpdateHeader(); err != nil {
		t.Fatal(err)
	}
	raw, err := txn.SerializeHex()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func payoutCode(err error) int {
	var pe *payoutError
	if errors.As(err, &pe) {
		return pe.code
	}
	return 0
}

func TestSendPayoutPaysTheCSV(t *testing.T) {
	dir := raceHist(t, "5.000999", "3.123456")
	sent := 0
	day, txid, err := sendPayout(dir, raceTxn(t, dir), func() (string, error) { sent++; return "txid", nil })
	if err != nil || day != raceDay || txid != "txid" || sent != 1 {
		t.Fatalf("day %q txid %q sent %d: %v", day, txid, sent, err)
	}
	if undistributed(dir, raceDay) {
		t.Fatal("day still undistributed")
	}
}

// A Calc between GET /payout and POST /reward changes the csv: the
// transaction built from the old one is refused, not broadcast.
func TestSendPayoutRefusesRecalculatedCSV(t *testing.T) {
	dir := raceHist(t, "4.000000", "3.000000")
	raw := raceTxn(t, dir)
	writeRaceCSV(t, dir, "4.000000", "2.500000")
	sent := 0
	_, _, err := sendPayout(dir, raw, func() (string, error) { sent++; return "txid", nil })
	if payoutCode(err) != http.StatusConflict || !errors.Is(err, rewards.ErrPayoutMismatch) || sent != 0 {
		t.Fatalf("code %d sent %d: %v", payoutCode(err), sent, err)
	}
	if !undistributed(dir, raceDay) {
		t.Fatal("refused payout marked the day")
	}
}

// A send that did not finish freezes the day: it is not offered again, and
// not paid again.
func TestPayoutPendingMarker(t *testing.T) {
	dir := raceHist(t, "4.000000", "3.000000")
	raw := raceTxn(t, dir)
	if err := os.WriteFile(filepath.Join(dir, raceDay+".txt.pending"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if undistributed(dir, raceDay) {
		t.Fatal("pending day reads as undistributed")
	}
	if _, _, err := nextPayout(dir); !errors.Is(err, errPayoutPending) {
		t.Fatalf("nextPayout: %v", err)
	}
	sent := 0
	if _, _, err := sendPayout(dir, raw, func() (string, error) { sent++; return "txid", nil }); payoutCode(err) != http.StatusConflict || sent != 0 {
		t.Fatalf("code %d sent %d: %v", payoutCode(err), sent, err)
	}
}

// A Calc never runs while a payout is in flight, and a payout waits for a
// running Calc rather than racing it.
func TestPayoutAndCalcNeverOverlap(t *testing.T) {
	dir := raceHist(t, "4.000000", "3.000000")
	var broadcasting, calcing, overlaps atomic.Int32
	hold := make(chan struct{})
	s := &Scheduler{Cycle: Cycle{Calc: func(context.Context, string) (CalcResult, error) {
		calcing.Store(1)
		defer calcing.Store(0)
		overlaps.Add(broadcasting.Load())
		<-hold
		writeRaceCSV(t, dir, "4.000000", "2.000000")
		return CalcResult{}, nil
	}}}
	SetScheduler(s)
	defer SetScheduler(nil)

	// A survey arrives while the payout broadcasts.
	calcDone := make(chan struct{})
	_, _, err := sendPayout(dir, raceTxn(t, dir), func() (string, error) {
		broadcasting.Store(1)
		defer broadcasting.Store(0)
		go func() { _ = s.RunOnce(context.Background(), "survey"); close(calcDone) }() //nolint:errcheck
		time.Sleep(50 * time.Millisecond)
		overlaps.Add(calcing.Load())
		return "txid", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	close(hold)
	<-calcDone
	if n := overlaps.Load(); n != 0 {
		t.Fatal("a Calc ran during the broadcast")
	}

	// A payout that arrives during a Calc waits for it, then sees its csv.
	dir2 := raceHist(t, "4.000000", "3.000000")
	raw := raceTxn(t, dir2)
	dir = dir2
	hold = make(chan struct{})
	calcDone = make(chan struct{})
	go func() { _ = s.RunOnce(context.Background(), "survey"); close(calcDone) }() //nolint:errcheck
	waitFor(t, func() bool { return calcing.Load() == 1 }, time.Second)
	payDone := make(chan error, 1)
	go func() {
		_, _, err := sendPayout(dir2, raw, func() (string, error) { return "txid", nil })
		payDone <- err
	}()
	select {
	case err := <-payDone:
		t.Fatalf("payout ran during a Calc: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(hold)
	<-calcDone
	if err := <-payDone; payoutCode(err) != http.StatusConflict {
		t.Fatalf("payout after the Calc rewrote the csv: %v", err)
	}
}
