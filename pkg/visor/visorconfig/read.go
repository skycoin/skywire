package visorconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skycoin/src/util/logging"
)

// Pkgpath is the path to the default skywire hypervisor config file
const Pkgpath = "/opt/skywire/skywire.json"

// ReadConfig reads the config file without opening or writing to it
func ReadConfig(path string) (*V1, error) {
	mLog := logging.NewMasterLogger()
	mLog.SetLevel(logrus.InfoLevel)

	f, err := os.ReadFile(path) //nolint
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	raw, err := ioutil.ReadAll(bytes.NewReader(f))
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	/*
		var conf map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return nil, fmt.Errorf("failed to obtain config version: %w", err)
		}
		//fmt.Println(result["users"])

	cc, err := NewCommon(mLog, path, "", nil)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, cc); err != nil {
		return nil, fmt.Errorf("failed to obtain config version: %w", err)
	}
	*/
	conf := &V1{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&conf); err != nil {
		return nil, fmt.Errorf("failed to decode json: %w", err)
	}
	if err := conf.ensureKeys(); err != nil {
		return nil, fmt.Errorf("%v: %w", ErrInvalidSK, err)
	}
	return conf, err
}
