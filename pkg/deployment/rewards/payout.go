// Package rewards pkg/deployment/rewards/payout.go c3-app-rewards
package rewards

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/coin"
	"github.com/skycoin/skycoin/src/util/droplet"

	"github.com/skycoin/skywire/pkg/visor/rewardconfig"
)

// SingleAddressCutoff is the first reward day that pays a single address only
// if it has never sent coins. See rewards/mainnet_rules.md, Skycoin Address.
var SingleAddressCutoff = time.Date(2026, time.November, 15, 0, 0, 0, 0, time.UTC)

// IPProofCutoff is the first reward day that pays a visor only if a deployment
// dmsg server signed its IP. See rewards/mainnet_rules.md, Per IP Limit.
var IPProofCutoff = time.Date(2026, time.December, 15, 0, 0, 0, 0, time.UTC)

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

// ErrPayoutMismatch marks a reward transaction whose outputs are not those of
// the payout CSV.
var ErrPayoutMismatch = errors.New("the transaction does not pay the payout csv")

// PayoutDroplets reads a payout CSV ("address, amount" rows) as
// scripts/rewards/sendrewards.sh hands it to createRawTransaction: the amount
// truncated to 3 decimals, and rows without an address or paying nothing
// left out. Amounts are in droplets, summed per address.
func PayoutDroplets(csv []byte) (map[cipher.Address]uint64, error) {
	want := map[cipher.Address]uint64{}
	sc := bufio.NewScanner(bytes.NewReader(csv))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		a, amount, _ := strings.Cut(strings.ReplaceAll(sc.Text(), " ", ""), ",")
		addr, err := cipher.DecodeBase58Address(a)
		if err != nil {
			continue
		}
		f, err := strconv.ParseFloat(amount, 64)
		if err != nil {
			return nil, fmt.Errorf("payout csv amount %q for %s: %w", amount, a, err)
		}
		// awk's int($2*1000) in sendrewards.sh, then %.3f.
		if n := math.Trunc(f * 1000); n > 0 {
			want[addr] += uint64(n) * 1000
		}
	}
	return want, sc.Err()
}

// CSVAddresses are the addresses of a reward csv, whatever their amounts.
func CSVAddresses(csv []byte) map[cipher.Address]bool {
	m := map[cipher.Address]bool{}
	sc := bufio.NewScanner(bytes.NewReader(csv))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		a, _, _ := strings.Cut(strings.ReplaceAll(sc.Text(), " ", ""), ",")
		if addr, err := cipher.DecodeBase58Address(a); err == nil {
			m[addr] = true
		}
	}
	return m
}

// CheckTransactionPaysExactly refuses a raw transaction (hex) unless its
// outputs pay each address of want exactly its droplets. One more output may
// take the change, to an address that is none of the day's payees.
func CheckTransactionPaysExactly(rawTxn string, want map[cipher.Address]uint64, payees map[cipher.Address]bool) error {
	txn, err := coin.DeserializeTransactionHex(strings.TrimSpace(rawTxn))
	if err != nil {
		return fmt.Errorf("decode transaction: %w", err)
	}
	got := map[cipher.Address]uint64{}
	for _, o := range txn.Out {
		got[o.Address] += o.Coins
	}
	var bad []string
	for addr, w := range want {
		switch g := got[addr]; {
		case g == w:
		case g == 0:
			bad = append(bad, fmt.Sprintf("%s is not paid (csv %s)", addr, coins(w)))
		default:
			bad = append(bad, fmt.Sprintf("%s is paid %s (csv %s)", addr, coins(g), coins(w)))
		}
	}
	var change []cipher.Address
	for addr, g := range got {
		if _, ok := want[addr]; ok {
			continue
		}
		if payees[addr] {
			bad = append(bad, fmt.Sprintf("%s is paid %s and is not in the csv", addr, coins(g)))
			continue
		}
		change = append(change, addr)
	}
	if len(change) > 1 {
		bad = append(bad, fmt.Sprintf("%d outputs outside the csv, only one change output is allowed", len(change)))
	}
	if len(bad) > 0 {
		slices.Sort(bad)
		return fmt.Errorf("%w: %s", ErrPayoutMismatch, strings.Join(bad, "; "))
	}
	return nil
}

func coins(d uint64) string {
	s, err := droplet.ToString(d)
	if err != nil {
		return strconv.FormatUint(d, 10) + " droplets"
	}
	return s
}
