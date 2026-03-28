// Package rewardconfig pkg/visor/rewardconfig/reward.go
package rewardconfig

import (
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

// CheckXpubDepth checks if an xpub key is at the expected BIP44 account level (depth 3).
// Returns a warning message if the key is at chain level (depth 4), or empty string if OK.
// This does not prevent setting the reward address, but warns that login verification
// will not work with a chain-level xpub.
func CheckXpubDepth(xpub string) string {
	pk, err := bip32.DeserializeEncodedPublicKey(xpub)
	if err != nil {
		return ""
	}
	if pk.Depth == 4 {
		return "WARNING: This xpub is at chain level (depth 4), not account level (depth 3). " +
			"The Skycoin web wallet shows the external chain xpub (path 0/0) which cannot " +
			"derive change chain addresses for login verification. " +
			"Export the account-level xpub with: skywire skycoin cli walletKeyExport WALLET -k xpub --path=0"
	}
	return ""
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
