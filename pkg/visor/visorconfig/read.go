package visorconfig

import (
	"bytes"
	//"regexp"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	//"github.com/skycoin/dmsg/pkg/disc"
	"os"

)

// Pkgpath is the path to the default skywire hypervisor config file
const Pkgpath = "/opt/skywire/skywire.json"
var r io.Reader


// Reader accepts io.Reader
func Reader(r io.Reader) (*V1, error) {
	raw, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	return ReadRaw(raw)
}

// ReadFile reads the config file without opening or writing to it
func ReadFile(path string) (*V1, error) {
	f, err := os.ReadFile(path) //nolint
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	raw, err := ioutil.ReadAll(bytes.NewReader(f))
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	return ReadRaw(raw)
}

// ReadRaw returns config from raw
func ReadRaw(raw []byte) (*V1, error) {

	cc, err := NewCommon(nil, nil)
	if err != nil {
		return nil, err
	}
	conf := MakeBaseConfig(cc, false, true, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed to create config template.")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&conf); err != nil {
		return nil, fmt.Errorf("failed to decode json: %w", err)
	}
	if err := conf.ensureKeys(); err != nil {
		return nil, fmt.Errorf("%v: %w", ErrInvalidSK, err)
	}
	return conf, nil
}
