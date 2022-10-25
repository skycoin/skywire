// Package visorconfig pkg/visor/visorconfig/read.go
package visorconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Pkgpath is the path to the default skywire hypervisor config file
const Pkgpath = "/opt/skywire/skywire.json"

// Reader accepts io.Reader
func Reader(r io.Reader, confPath string) (*V1, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	return ReadRaw(raw, confPath)
}

// ReadFile reads the config file without opening or writing to it
func ReadFile(confPath string) (*V1, error) {
	f, err := os.ReadFile(confPath) //nolint
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	raw, err := io.ReadAll(bytes.NewReader(f))
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	return ReadRaw(raw, confPath)
}

// ReadRaw returns config from raw
func ReadRaw(raw []byte, confPath string) (*V1, error) {

	cc, err := NewCommon(nil, confPath, nil)
	if err != nil {
		return nil, err
	}
	conf := MakeBaseConfig(cc, false, true, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create config template")
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
