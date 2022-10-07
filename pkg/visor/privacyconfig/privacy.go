package privacyconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Privacy represents the json-encoded contents of the privacy.json file
type Privacy struct {
	DisplayNodeIP bool   `json:"display_node_ip"`
	RewardAddress string `json:"reward_address,omitempty"`
}

// SetReward sets the reward address in privacy config file
func SetReward(confP *Privacy, out string) (*Privacy, error) {
	j, err := json.MarshalIndent(confP, "", "\t")
	if err != nil {
		return nil, fmt.Errorf("Could not marshal json. err=%v", err)
	}
	err = os.WriteFile(out, j, 0644) //nolint
	if err != nil {
		return nil, fmt.Errorf("Failed to write config to file. err=%v", err)
	}
	return confP, nil
}

// GetReward gets the contents of privacy config file
func GetReward(out string) (*Privacy, error) {
	j, err := os.ReadFile(filepath.Clean(out))
	if err != nil {
		return nil, fmt.Errorf("Failed to read config file. err=%v", err)
	}

	var jsonOutput Privacy
	err = json.Unmarshal(j, &jsonOutput)
	if err != nil {
		return nil, fmt.Errorf("ailed to unmarshal json: %v", err)
	}
	return &jsonOutput, nil
}
