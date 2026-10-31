// Package rewardconfig pkg/visor/rewardconfig/reward.go c3-vis-core
package rewardconfig

import (
	"errors"
	"fmt"
	"strings"

	coincipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/bip32"
)

// Reward represents the json-encoded contents of the reward.json file
type Reward struct {
	RewardAddress string `json:"reward_address,omitempty"`
}

// ValidateRewardAddress validates a reward address string.
// Accepts either a skycoin base58 address or a bip32 xpub key.
// Returns the canonical string form of the address/xpub, whether it is an xpub,
// and any validation error.
func ValidateRewardAddress(addr string) (canonical string, isXpub bool, err error) {
	addr = strings.TrimSpace(addr)
	// Try xpub first (starts with "xpub")
	if strings.HasPrefix(addr, "xpub") {
		pk, err := bip32.DeserializeEncodedPublicKey(addr)
		if err != nil {
			return "", false, fmt.Errorf("invalid xpub key: %w", err)
		}
		_ = pk
		return addr, true, nil
	}
	// Try skycoin address
	cAddr, err := coincipher.DecodeBase58Address(addr)
	if err != nil {
		return "", false, fmt.Errorf("invalid skycoin address: %w", err)
	}
	return cAddr.String(), false, nil
}

// ErrSingleAddress is returned when a plain address is given where only an
// account xpub is accepted. A single address shows every reward it was paid
// to anyone, and spending from it links them; an xpub pays a fresh address.
var ErrSingleAddress = errors.New("a single skycoin address is no longer accepted as a reward address: " +
	"set your wallet's account xpub instead, so each reward goes to a fresh address. " +
	"In the Skycoin desktop wallet or skycoin-web, copy the wallet's Account xPub")

// ErrNotAccountXpub is returned for an xpub that is not a BIP44 account xpub.
// Rewards derive m/44'/8000'/0'/0/i from it, so a chain xpub (depth 4) would
// pay addresses its wallet never shows.
var ErrNotAccountXpub = errors.New("this xpub is not a wallet's account xpub (depth 3). " +
	"In the Skycoin desktop wallet or skycoin-web, copy the Account xPub, not the xPub of the External Chain. " +
	"From the CLI: skywire skycoin cli walletKeyExport WALLET -k xpub --path=0")

// AccountXpubDepth is the depth of a BIP44 account xpub, m/44'/coin'/account'.
const AccountXpubDepth = 3

// ValidateRewardXpub validates a reward address being set, which must be a
// BIP44 account xpub. Addresses already set are still read with ValidateRewardAddress.
func ValidateRewardXpub(addr string) (string, error) {
	canonical, isXpub, err := ValidateRewardAddress(addr)
	if err != nil {
		return "", err
	}
	if !isXpub {
		return "", ErrSingleAddress
	}
	if CheckXpubDepth(canonical) != "" {
		return "", ErrNotAccountXpub
	}
	return canonical, nil
}

// CheckXpubDepth returns a message when xpub is not at the BIP44 account level
// (depth 3), or an empty string if it is. It reports on xpubs set before
// ValidateRewardXpub rejected other depths.
func CheckXpubDepth(xpub string) string {
	pk, err := bip32.DeserializeEncodedPublicKey(xpub)
	if err != nil {
		return ""
	}
	if pk.Depth != AccountXpubDepth {
		return fmt.Sprintf("this xpub is at depth %d, not the account level (depth 3), "+
			"so rewards would go to addresses its wallet never shows. Set the wallet's Account xPub instead "+
			"(skywire skycoin cli walletKeyExport WALLET -k xpub --path=0)", pk.Depth)
	}
	return ""
}

// RewardAddresses derives the first n external chain addresses of an account
// xpub, m/44'/8000'/0'/0/0 onward. Rewards are paid to these, in order, each to
// an address not used before, so they are what the wallet should list.
func RewardAddresses(xpub string, n uint32) ([]string, error) {
	if msg := CheckXpubDepth(xpub); msg != "" {
		return nil, errors.New(msg)
	}
	addrs := make([]string, 0, n)
	for i := uint32(0); i < n; i++ {
		a, err := DeriveExternalAddressFromXpub(xpub, i)
		if err != nil {
			return nil, err
		}
		addrs = append(addrs, a)
	}
	return addrs, nil
}

// DeriveExternalAddressFromXpub derives a reward address from an account xpub key.
// Uses the BIP44 external chain (m/account'/0/index) — the same chain used for
// reward distribution addresses.
func DeriveExternalAddressFromXpub(xpub string, index uint32) (string, error) {
	accountKey, err := bip32.DeserializeEncodedPublicKey(xpub)
	if err != nil {
		return "", fmt.Errorf("invalid xpub: %w", err)
	}

	// Derive external chain key: account_xpub → child(0)
	externalChainKey, err := accountKey.NewPublicChildKey(0) // 0 = external chain
	if err != nil {
		return "", fmt.Errorf("derive external chain: %w", err)
	}

	// Derive address at index
	addrKey, err := externalChainKey.NewPublicChildKey(index)
	if err != nil {
		return "", fmt.Errorf("derive address at index %d: %w", index, err)
	}

	cpk, err := coincipher.NewPubKey(addrKey.Key)
	if err != nil {
		return "", fmt.Errorf("invalid derived public key: %w", err)
	}

	addr := coincipher.AddressFromPubKey(cpk)
	return addr.String(), nil
}

// DeriveLoginAddressFromXpub derives a login verification address from an account xpub key.
// Uses the BIP44 change chain (m/account'/1/index) so login addresses don't
// collide with reward addresses (which use the external chain m/account'/0/i).
//
// The xpub must be at the account level (depth 3, BIP44 path m/44'/coin'/account').
// The external chain xpub shown in the Skycoin web wallet GUI is depth 4 and will
// not work — use 'skywire skycoin cli walletKeyExport WALLET -k xpub --path=0'
// to export the correct account-level xpub.
func DeriveLoginAddressFromXpub(xpub string, index uint32) (string, error) {
	// Parse the account-level xpub
	accountKey, err := bip32.DeserializeEncodedPublicKey(xpub)
	if err != nil {
		return "", fmt.Errorf("invalid xpub: %w", err)
	}

	// Verify this is an account-level key (depth 3), not a chain-level key (depth 4).
	// Depth 4 means the user provided the external or change chain xpub directly,
	// which cannot be used to derive the change chain for login verification.
	if accountKey.Depth == 4 {
		return "", fmt.Errorf("xpub is at chain level (depth 4), not account level (depth 3); " +
			"use 'skywire skycoin cli walletKeyExport WALLET -k xpub --path=0' to get the account-level xpub")
	}

	// Derive change chain key: account_xpub → child(1)
	changeChainKey, err := accountKey.NewPublicChildKey(1) // 1 = change chain
	if err != nil {
		return "", fmt.Errorf("derive change chain: %w", err)
	}

	// Derive address at index: change_chain → child(index)
	addrKey, err := changeChainKey.NewPublicChildKey(index)
	if err != nil {
		return "", fmt.Errorf("derive address at index %d: %w", index, err)
	}

	// Convert to skycoin address
	cpk, err := coincipher.NewPubKey(addrKey.Key)
	if err != nil {
		return "", fmt.Errorf("invalid derived public key: %w", err)
	}

	addr := coincipher.AddressFromPubKey(cpk)
	return addr.String(), nil
}
