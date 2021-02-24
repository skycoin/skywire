package service

import (
	"encoding/json"
	"io/ioutil"
	"os"

	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/skycoin/skywire/pkg/snet"
)

// Config defines configuration for transport setup
type Config struct {
	PK   cipher.PubKey   `json:"public_key"`
	SK   cipher.SecKey   `json:"secret_key"`
	Dmsg snet.DmsgConfig `json:"dmsg"`
}

// MustReadConfig reads and decodes config from file. If there is an error,
// it reports the error and exits application
func MustReadConfig(filename string, log *logging.Logger) Config {
	rdr, err := os.Open(filename)
	if err != nil {
		log.Fatalf("Failed to open config: %v", err)
	}
	conf := Config{}
	raw, err := ioutil.ReadAll(rdr)
	if err != nil {
		log.Fatalf("Failed to read config: %v", err)
	}
	if err := json.Unmarshal(raw, &conf); err != nil {
		log.WithField("raw", string(raw)).Fatalf("Failed to decode config: %s", err)
	}
	return conf
}
