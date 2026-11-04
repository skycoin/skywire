package rewards

import (
	"errors"
	"testing"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/coin"
	"github.com/stretchr/testify/require"
)

const (
	payoutSpent   = "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6"
	payoutUnspent = "zU361Dk1sFqBMcTdMQbByfkJNzJyVPcT9h"
	payoutDerived = "2VfPAT8tZMwoypWSZgdhgCiKrYdYoSxy1dE"
	payoutPK1     = "03394072dd83acfd34f359ce16243bef654d745ac367fe3dd30527f7cb4d827030"
	payoutPK2     = "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"
)

var payoutShares = []byte("Skycoin Address, Skywire Public Key, Reward Shares, Reward SKY Amount, IP, Architecture, UUID, Interfaces, Country, XPub\n" +
	payoutSpent + ", " + payoutPK1 + ", 1.000000, 2.000000, 1.2.3.4, amd64, u1, eth0, US,  \n" +
	payoutSpent + ", " + payoutPK2 + ", 1.000000, 2.000000, 1.2.3.5, amd64, u2, eth0, US,  \n" +
	payoutUnspent + ", 02aa, 1.000000, 3.000000, 1.2.3.6, arm64, u3, eth0, DE,  \n" +
	payoutDerived + ", 02bb, 1.000000, 5.000000, 1.2.3.7, arm64, u4, eth0, DE, xpub6CcSesQqGHf226EZjBZjRTkTrTPmQ8oCAjAHtDBNpYTj1Yu1v46V5TaYEZeSE8iyHEj3eXs7ozZok4ux9b5uLHZRtHacskodD7B53xRG6sE \n")

var payoutTxn = []byte("Skycoin Address, Reward Amount\n" +
	payoutDerived + ", 5.000000\n" +
	payoutSpent + ", 4.000000\n" +
	payoutUnspent + ", 3.000000\n")

// payoutNode answers as a node would, and counts the questions.
func payoutNode(asked map[string]int, down bool) func(string) (bool, error) {
	return func(addr string) (bool, error) {
		asked[addr]++
		if down {
			return false, errors.New("node down")
		}
		// Spent says yes for every address but the unspent one, so a check
		// of an xpub derived address would show up as a skipped payout.
		return addr != payoutUnspent, nil
	}
}

func TestFilterSpentPayouts(t *testing.T) {
	asked := map[string]int{}
	out, skipped, err := FilterSpentPayouts("2026-11-20", payoutTxn, payoutShares, payoutNode(asked, false))
	require.NoError(t, err)
	require.Equal(t, "Skycoin Address, Reward Amount\n"+payoutDerived+", 5.000000\n"+payoutUnspent+", 3.000000\n", string(out))
	require.Len(t, skipped, 1)
	require.Equal(t, SkippedPayout{Day: "2026-11-20", PKs: []string{payoutPK1, payoutPK2}, Address: payoutSpent, Amount: "4.000000"}, skipped[0])
	require.Contains(t, skipped[0].String(), payoutPK2)
	require.Zero(t, asked[payoutDerived], "an xpub derived address is not a single address")

	_, _, err = FilterSpentPayouts("2026-11-20", payoutTxn, payoutShares, payoutNode(map[string]int{}, true))
	require.ErrorContains(t, err, "node down")
}

func payoutRawTxn(t *testing.T, addrs ...string) string {
	t.Helper()
	var txn coin.Transaction
	for _, a := range addrs {
		txn.Out = append(txn.Out, coin.TransactionOutput{Address: cipher.MustDecodeBase58Address(a), Coins: 1e6, Hours: 1})
	}
	s, err := txn.SerializeHex()
	require.NoError(t, err)
	return s
}

func TestCheckTransactionPayouts(t *testing.T) {
	require.NoError(t, CheckTransactionPayouts(payoutRawTxn(t, payoutDerived, payoutUnspent), payoutShares, payoutNode(map[string]int{}, false)))

	err := CheckTransactionPayouts(payoutRawTxn(t, payoutUnspent, payoutSpent), payoutShares, payoutNode(map[string]int{}, false))
	require.ErrorContains(t, err, payoutSpent)
	require.ErrorContains(t, err, payoutPK1)

	err = CheckTransactionPayouts(payoutRawTxn(t, payoutUnspent), payoutShares, payoutNode(map[string]int{}, true))
	require.ErrorContains(t, err, "node down")

	require.Error(t, CheckTransactionPayouts("nothex", payoutShares, payoutNode(map[string]int{}, false)))
}
