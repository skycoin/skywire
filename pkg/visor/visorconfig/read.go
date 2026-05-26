//go:build !js

// Package visorconfig pkg/visor/visorconfig/read.go
//
// Reader / ReadFile / ReadRaw load a V1 config from disk or an
// io.Reader. All three pull encoding/json and os.ReadFile, neither
// of which is reachable from the install-page WASM — browsers
// don't have filesystems, and TinyGo's stdlib can't link
// encoding/json's reflect runtime helpers anyway. Build-tag-gated
// off the WASM path; callers under js construct V1 in memory via
// genvisor.Generate.
package visorconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

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
	//nolint:gosec
	f, err := os.ReadFile(confPath)
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
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&conf); err != nil {
		return nil, fmt.Errorf("failed to decode json: %w", err)
	}
	if err := conf.ensureKeys(); err != nil {
		return nil, fmt.Errorf("%v: %w", ErrInvalidSK, err)
	}
	return conf, nil
}
