//go:build !js

// Package visorconfig pkg/visor/visorconfig/common_native.go c3-vis-core
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
	// 0640, not 0644: this config file holds the visor's SECRET KEY (Common.SK).
	// World-readable (0644) let any local user read the identity and impersonate
	// the visor. The visor writes/reads it as its own service user (or root), so
	// owner+group access is sufficient; unprivileged desktop helpers (the systray
	// tray) talk to the visor over RPC by address and never read this file. Note
	// os.WriteFile only applies this mode when CREATING the file — pre-existing
	// 0644 configs must be tightened out-of-band (package postinstall chmod).
	const filePerm = 0640
	return os.WriteFile(c.path, raw, filePerm)
}
