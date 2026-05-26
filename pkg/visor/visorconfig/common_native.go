//go:build !js

// Package visorconfig pkg/visor/visorconfig/common_native.go
//
// Native (non-WASM) implementation of Common.flush — writes a
// JSON-encoded config back to its on-disk path. encoding/json
// imports the reflect runtime helpers TinyGo's stdlib doesn't
// provide, so this code path is tagged off the WASM build to
// keep `tinygo build -target wasm` viable for the install-page
// generator. The browser-side caller can't write files anyway.
package visorconfig

import (
	"encoding/json"
	"os"
)

func (c *Common) flush(v interface{}) (err error) {
	switch c.path {
	case "":
		return ErrNoConfigPath
	case Stdin:
		return nil
	}

	log := c.log.
		PackageLogger("visor:config").
		WithField("filepath", c.path).
		WithField("config_version", c.Version)
	log.Info("Flushing config to file.")
	defer func() {
		if err != nil {
			log.WithError(err).Error("Failed to flush config to file.")
		}
	}()

	raw, err := json.MarshalIndent(v, "", "\t")
	if err != nil {
		return err
	}
	const filePerm = 0644
	return os.WriteFile(c.path, raw, filePerm)
}
