// Package rewardconfig pkg/visor/rewardconfig/reward.go
package rewardconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	err = os.WriteFile(out, j, 0644) //nolint
	if err != nil {
		return nil, fmt.Errorf("failed to write config to file. err=%v", err)
	}
	return confP, nil
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
