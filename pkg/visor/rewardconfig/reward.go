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
