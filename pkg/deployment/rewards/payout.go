// Package rewards pkg/deployment/rewards/payout.go c3-app-rewards
package rewards

import (
	"bufio"
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/skycoin/skycoin/src/coin"

	"github.com/skycoin/skywire/pkg/visor/rewardconfig"
)

// SingleAddressCutoff is the first reward day that pays a single address only
// if it has never sent coins. See rewards/mainnet_rules.md, Skycoin Address.
var SingleAddressCutoff = time.Date(2026, time.November, 15, 0, 0, 0, 0, time.UTC)

// spentCheckApplies reports whether a reward day (2006-01-02) is on or after
// SingleAddressCutoff. A day that does not parse is checked.
func spentCheckApplies(day string) bool {
	t, err := time.Parse(time.DateOnly, day)
	return err != nil || !t.Before(SingleAddressCutoff)
}

// SkippedPayout is a payout to a single address left out at distribution
// because the address has sent coins.
type SkippedPayout struct {
	Day     string
	PKs     []string
	Address string
	Amount  string
}

func (s SkippedPayout) String() string {
	return fmt.Sprintf("day %s address %s amount %s pk %s: reward address has sent coins, payout skipped", s.Day, s.Address, s.Amount, strings.Join(s.PKs, " "))
}

// NodeAddressSpent reports whether an address has ever sent coins, using a skycoin node API.
func NodeAddressSpent(nodeURL string) func(addr string) (bool, error) {
	c := &http.Client{Timeout: 30 * time.Second}
	return func(addr string) (bool, error) {
		e, err := rewardconfig.FetchAddressExposure(c, nodeURL, addr)
		return e.Spent, err
	}
}

// SingleAddressPKs reads a day's shares CSV and maps each single (not xpub
// derived) reward address to the visors paid there. The first column is the
// address, the second the visor PK and the last the xpub, empty for a single address.
func SingleAddressPKs(sharesCSV []byte) map[string][]string {
	m := map[string][]string{}
	sc := bufio.NewScanner(bytes.NewReader(sharesCSV))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		f := strings.Split(sc.Text(), ",")
		if len(f) < 3 {
			continue
		}
		addr, pk, xpub := strings.TrimSpace(f[0]), strings.TrimSpace(f[1]), strings.TrimSpace(f[len(f)-1])
		if xpub != "" {
			continue
		}
		if _, xpub, err := rewardconfig.ValidateRewardAddress(addr); err != nil || xpub {
			continue
		}
		m[addr] = append(m[addr], pk)
	}
	return m
}

// FilterSpentPayouts drops from a day's reward transaction CSV ("address,
// amount" rows) every payout to a single address that has sent coins, from
// SingleAddressCutoff on. It fails, rather than guess, when spent cannot answer.
func FilterSpentPayouts(day string, txnCSV, sharesCSV []byte, spent func(string) (bool, error)) ([]byte, []SkippedPayout, error) {
	if !spentCheckApplies(day) {
		return txnCSV, nil, nil
	}
	singles := SingleAddressPKs(sharesCSV)
	var out bytes.Buffer
	var skipped []SkippedPayout
	sc := bufio.NewScanner(bytes.NewReader(txnCSV))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		addr, amount, _ := strings.Cut(line, ",")
		addr, amount = strings.TrimSpace(addr), strings.TrimSpace(amount)
		if pks, ok := singles[addr]; ok {
			s, err := spent(addr)
			if err != nil {
				return nil, nil, fmt.Errorf("check whether reward address %s has sent coins: %w", addr, err)
			}
			if s {
				skipped = append(skipped, SkippedPayout{Day: day, PKs: pks, Address: addr, Amount: amount})
				continue
			}
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.Bytes(), skipped, sc.Err()
}

// CheckTransactionPayouts refuses a raw transaction (hex) for a reward day on
// or after SingleAddressCutoff that pays a single reward address of the day
// that has sent coins, or when spent cannot answer.
func CheckTransactionPayouts(day, rawTxn string, sharesCSV []byte, spent func(string) (bool, error)) error {
	txn, err := coin.DeserializeTransactionHex(strings.TrimSpace(rawTxn))
	if err != nil {
		return fmt.Errorf("decode transaction: %w", err)
	}
	if !spentCheckApplies(day) {
		return nil
	}
	singles := SingleAddressPKs(sharesCSV)
	var bad []string
	for _, o := range txn.Out {
		addr := o.Address.String()
		pks, ok := singles[addr]
		if !ok {
			continue
		}
		s, err := spent(addr)
		if err != nil {
			return fmt.Errorf("check whether reward address %s has sent coins: %w", addr, err)
		}
		if s {
			bad = append(bad, fmt.Sprintf("%s (pk %s)", addr, strings.Join(pks, " ")))
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("the transaction pays reward addresses that have sent coins, not broadcast: %s", strings.Join(bad, ", "))
	}
	return nil
}
