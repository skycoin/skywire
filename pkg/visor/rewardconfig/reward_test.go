// Package rewardconfig pkg/visor/rewardconfig/reward_test.go
package rewardconfig

import (
	"errors"
	"strings"
	"testing"

	coincipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/bip44"
)

func testXpubs(t *testing.T) (account, chain string) {
	t.Helper()
	coin, err := bip44.NewCoin([]byte("skywire reward xpub test seed 0123456789"), bip44.CoinTypeSkycoin)
	if err != nil {
		t.Fatal(err)
	}
	acct, err := coin.Account(0)
	if err != nil {
		t.Fatal(err)
	}
	ap, err := acct.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	ext, err := acct.External()
	if err != nil {
		t.Fatal(err)
	}
	ep, err := ext.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	return ap.String(), ep.String()
}

func TestValidateRewardXpub(t *testing.T) {
	account, chain := testXpubs(t)

	if got, err := ValidateRewardXpub("  " + account + "\n"); err != nil || got != account {
		t.Fatalf("account xpub: %q, %v", got, err)
	}
	if _, err := ValidateRewardXpub(chain); !errors.Is(err, ErrNotAccountXpub) {
		t.Fatalf("chain xpub: %v", err)
	}
	if CheckXpubDepth(chain) == "" || CheckXpubDepth(account) != "" {
		t.Fatal("depth warning is for chain xpubs only")
	}
	if _, err := ValidateRewardXpub("2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6"); !errors.Is(err, ErrSingleAddress) {
		t.Fatalf("single address: %v", err)
	}
	if _, err := ValidateRewardXpub("xpubnotakey"); err == nil || errors.Is(err, ErrSingleAddress) {
		t.Fatalf("bad xpub: %v", err)
	}
	// An address already set is still readable.
	if _, isXpub, err := ValidateRewardAddress("2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6"); err != nil || isXpub {
		t.Fatalf("existing address: %v %v", isXpub, err)
	}
	if !strings.HasPrefix(account, "xpub") {
		t.Fatalf("unexpected xpub form %q", account)
	}
}

func TestRewardAddresses(t *testing.T) {
	account, chain := testXpubs(t)

	addrs, err := RewardAddresses(account, 3)
	if err != nil || len(addrs) != 3 {
		t.Fatalf("account xpub: %v %v", addrs, err)
	}
	coin, err := bip44.NewCoin([]byte("skywire reward xpub test seed 0123456789"), bip44.CoinTypeSkycoin)
	if err != nil {
		t.Fatal(err)
	}
	acct, err := coin.Account(0)
	if err != nil {
		t.Fatal(err)
	}
	ext, err := acct.External()
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range addrs {
		k, err := ext.NewPublicChildKey(uint32(i))
		if err != nil {
			t.Fatal(err)
		}
		want := coincipher.AddressFromPubKey(coincipher.MustNewPubKey(k.Key)).String()
		if a != want {
			t.Fatalf("address %d: %s, want %s", i, a, want)
		}
	}
	if _, err := RewardAddresses(chain, 3); err == nil {
		t.Fatal("chain xpub must not derive reward addresses")
	}
}
