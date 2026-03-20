// Package rewardconfig pkg/visor/rewardconfig/reward.go
package rewardconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	coincipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/bip32"
)

// Reward represents the json-encoded contents of the reward.json file
type Reward struct {
	RewardAddress string `json:"reward_address,omitempty"`
}

// SetReward sets the reward address in Reward config file
func SetReward(confP *Reward, out string) (*Reward, error) {
	j, err := json.MarshalIndent(confP, "", "\t")
	if err != nil {
		return nil, fmt.Errorf("could not marshal json. err=%v", err)
	}
	err = os.WriteFile(out, j, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to write config to file. err=%v", err)
	}
	return confP, nil
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

// GetReward gets the contents of reward config file
func GetReward(out string) (*Reward, error) {
	j, err := os.ReadFile(filepath.Clean(out))
	if err != nil {
		return nil, fmt.Errorf("failed to read config file. err=%v", err)
	}

	var jsonOutput Reward
	err = json.Unmarshal(j, &jsonOutput)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal json: %v", err)
	}
	return &jsonOutput, nil
}
