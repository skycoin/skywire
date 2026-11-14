package rewardsserver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/coin"
)

func TestNextPayoutSkipsSpentSingleAddresses(t *testing.T) {
	const spent = "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6"
	const fresh = "zU361Dk1sFqBMcTdMQbByfkJNzJyVPcT9h"
	const pk = "03394072dd83acfd34f359ce16243bef654d745ac367fe3dd30527f7cb4d827030"
	dir := t.TempDir()
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	shares := "Skycoin Address, Skywire Public Key, Reward Shares, Reward SKY Amount, IP, Architecture, UUID, Interfaces, Country, XPub\n" +
		spent + ", " + pk + ", 1.0, 4.0, 1.2.3.4, amd64, u, eth0, US,  \n" +
		fresh + ", 02aa, 1.0, 3.0, 1.2.3.5, amd64, u, eth0, US,  \n"
	txn := "Skycoin Address, Reward Amount\n" + spent + ", 4.000000\n" + fresh + ", 3.000000\n"
	// The first day was sent already, so the backlog starts at the second.
	for _, d := range []string{"2026-11-15", "2026-11-16"} {
		write(d+"_rewardtxn0.csv", txn)
		write(d+"_shares.csv", shares)
	}
	write("2026-11-15.txt", "sometxid\n")

	down := false
	old := payoutAddressSpent
	payoutAddressSpent = func(addr string) (bool, error) {
		if down {
			return false, errors.New("node down")
		}
		return addr == spent, nil
	}
	defer func() { payoutAddressSpent = old }()

	day, csv, err := nextPayout(dir)
	if err != nil || day != "2026-11-16" {
		t.Fatalf("nextPayout: %q %v", day, err)
	}
	if strings.Contains(string(csv), spent) || !strings.Contains(string(csv), fresh) {
		t.Fatalf("payout csv:\n%s", csv)
	}
	skippedLog, err := os.ReadFile(filepath.Join(dir, "2026-11-16_skipped.txt")) //nolint:gosec
	if err != nil || !strings.Contains(string(skippedLog), pk) || !strings.Contains(string(skippedLog), spent) {
		t.Fatalf("skipped log: %q %v", skippedLog, err)
	}

	var tx coin.Transaction
	tx.Out = []coin.TransactionOutput{{Address: cipher.MustDecodeBase58Address(spent), Coins: 1e6, Hours: 1}}
	raw, err := tx.SerializeHex()
	if err != nil {
		t.Fatal(err)
	}
	if err := checkPayoutTxn(dir, day, raw); err == nil || !strings.Contains(err.Error(), spent) {
		t.Fatalf("transaction paying a spent address: %v", err)
	}

	down = true
	if _, _, err := nextPayout(dir); err == nil {
		t.Fatal("node down: payout csv served")
	}
	if err := checkPayoutTxn(dir, day, raw); err == nil {
		t.Fatal("node down: transaction accepted")
	}

	write("2026-11-16.txt", "othertxid\n")
	if _, _, err := nextPayout(dir); !errors.Is(err, errNoPayout) {
		t.Fatalf("all sent: %v", err)
	}

	// A day before the cutoff pays every single address, without asking the node.
	write("2026-10-30_rewardtxn0.csv", txn)
	write("2026-10-30_shares.csv", shares)
	if day, csv, err := nextPayout(dir); err != nil || day != "2026-10-30" || string(csv) != txn {
		t.Fatalf("before cutoff: %q %v\n%s", day, err, csv)
	}
	if err := checkPayoutTxn(dir, "2026-10-30", raw); err != nil {
		t.Fatalf("before cutoff: transaction refused: %v", err)
	}
}
