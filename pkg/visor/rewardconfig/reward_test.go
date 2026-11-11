// Package rewardconfig pkg/visor/rewardconfig/reward_test.go
package rewardconfig

import (
	"errors"
	"strings"
	"testing"

	coincipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/bip32"
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

func TestValidateRewardSetting(t *testing.T) {
	account, chain := testXpubs(t)
	const addr = "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6"

	if _, _, err := ValidateRewardSetting(addr, false); !errors.Is(err, ErrSingleAddress) {
		t.Fatalf("single address without opt-in: %v", err)
	}
	if got, isXpub, err := ValidateRewardSetting(" "+addr, true); err != nil || isXpub || got != addr {
		t.Fatalf("single address with opt-in: %q %v %v", got, isXpub, err)
	}
	for _, risk := range []bool{false, true} {
		if got, isXpub, err := ValidateRewardSetting(account, risk); err != nil || !isXpub || got != account {
			t.Fatalf("account xpub, opt-in %v: %q %v %v", risk, got, isXpub, err)
		}
		if _, _, err := ValidateRewardSetting(chain, risk); !errors.Is(err, ErrNotAccountXpub) {
			t.Fatalf("chain xpub, opt-in %v: %v", risk, err)
		}
	}
}

func TestRewardAddressesSkipImpossibleChild(t *testing.T) {
	account, _ := testXpubs(t)
	want, err := RewardAddresses(account, 4)
	if err != nil {
		t.Fatal(err)
	}

	real := deriveChild
	t.Cleanup(func() { deriveChild = real })
	deriveChild = func(k *bip32.PublicKey, i uint32) (*bip32.PublicKey, error) {
		if i == 1 {
			return nil, bip32.NewImpossibleChildError(errors.New("test"), i)
		}
		return real(k, i)
	}

	if _, err := DeriveExternalAddressFromXpub(account, 1); !errors.Is(err, ErrNoAddressAtIndex) {
		t.Fatalf("index 1: %v, want ErrNoAddressAtIndex", err)
	}
	got, err := RewardAddresses(account, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != want[0] || got[1] != want[2] || got[2] != want[3] {
		t.Fatalf("got %v, want indexes 0, 2 and 3 of %v", got, want)
	}
}

func TestValidateRewardSettingNonHardenedAccount(t *testing.T) {
	// m/44'/8000'/0 is at depth 3 like an account, but its last step is not hardened.
	master, err := bip32.NewMasterKey([]byte("skywire reward xpub test seed 0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	k := master
	for _, i := range []uint32{44 + bip32.FirstHardenedChild, 8000 + bip32.FirstHardenedChild} {
		if k, err = k.NewPrivateChildKey(i); err != nil {
			t.Fatal(err)
		}
	}
	kp, err := k.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := kp.NewPublicChildKey(0)
	if err != nil {
		t.Fatal(err)
	}
	if pub.Depth != AccountXpubDepth {
		t.Fatalf("depth %d", pub.Depth)
	}
	if _, _, err := ValidateRewardSetting(pub.String(), false); !errors.Is(err, ErrNotAccountXpub) {
		t.Fatalf("non-hardened account: %v, want ErrNotAccountXpub", err)
	}
}
