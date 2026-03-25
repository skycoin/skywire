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
// Returns the canonical string form of the address/xpub and whether it is an xpub.
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

// DeriveLoginAddressFromXpub derives a login verification address from an account xpub key.
// Uses the BIP44 change chain (m/account'/1/index) so login addresses don't
// collide with reward addresses (which use the external chain m/account'/0/i).
//
// TODO: This should be moved to github.com/skycoin/skycoin/src/cipher/bip44
// as a proper utility function once the login system is stable.
func DeriveLoginAddressFromXpub(xpub string, index uint32) (string, error) {
	// Parse the account-level xpub
	accountKey, err := bip32.DeserializeEncodedPublicKey(xpub)
	if err != nil {
		return "", fmt.Errorf("invalid xpub: %w", err)
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
