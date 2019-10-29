package ptycfg

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/SkycoinProject/dmsg/cipher"
)

type Keys struct {
	Seed   string        `json:"seed"`
	PubKey cipher.PubKey `json:"public_key"`
	SecKey cipher.SecKey `json:"secret_key"`
}

func ReadKeys(fileName string) (Keys, error) {
	f, err := os.Open(fileName)
	if err != nil {
		return Keys{}, err
	}
	defer func() { _ = f.Close() }() //nolint:errcheck

	var keys Keys
	err = json.NewDecoder(f).Decode(&keys)
	return keys, err
}

func WriteKeys(fileName, seed string) (Keys, error) {
	fileName, err := filepath.Abs(fileName)
	if err != nil {
		return Keys{}, err
	}

	// Ensure conf directory exists.
	if err := os.MkdirAll(filepath.Dir(fileName), 0750); err != nil {
		return Keys{}, err
	}

	// Generate keys.
	pk, sk, err := cipher.GenerateDeterministicKeyPair([]byte(seed))
	if err != nil {
		return Keys{}, err
	}
	_ = os.Rename(fileName, fileName+".old") //nolint:errcheck
	f, err := os.Create(fileName)
	if err != nil {
		return Keys{}, err
	}
	keys := Keys{
		Seed:   seed,
		PubKey: pk,
		SecKey: sk,
	}
	return keys, json.NewEncoder(f).Encode(&keys)
}
