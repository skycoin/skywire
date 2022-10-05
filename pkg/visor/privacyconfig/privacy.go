package privacyconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	coincipher "github.com/skycoin/skycoin/src/cipher"
)

// Privacy represents the json-encoded contents of the privacy.json file
type Privacy struct {
	DisplayNodeIP bool               `json:"display_node_ip"`
	RewardAddress coincipher.Address `json:"reward_address,omitempty"`
}

// SetReward sets the reward address in privacy config file
func SetReward(confP Privacy, out, pathStr string) ([]byte, error) {
	// Print results.
	j, err := json.MarshalIndent(confP, "", "\t")
	if err != nil {
		return nil, fmt.Errorf("Could not marshal json. err=%v", err)
	}
	if _, err := os.Stat(pathStr); os.IsNotExist(err) {
		return nil, fmt.Errorf("local directory not found ; run skywire first to create this path. err=%v", err)
	}
	err = os.WriteFile(out, j, 0644) //nolint
	if err != nil {
		return nil, fmt.Errorf("Failed to write config to file. err=%v", err)
	}
	return j, nil
}

// GetReward gets the contents of privacy config file
func GetReward(out string) ([]byte, error) {
	j, err := os.ReadFile(filepath.Clean(out))
	if err != nil {
		return nil, fmt.Errorf("Failed to read config file. err=%v", err)
	}
	return j, nil
}
