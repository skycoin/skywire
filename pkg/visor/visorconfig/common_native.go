//go:build !tinygo

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
	"bytes"
	"encoding/json"
	"os"
	"reflect"
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

	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	// Read-modify-WRITE. Marshaling the struct alone ERASES every key
	// the file holds that this binary has no field for — one restart on
	// a build predating a config key silently deletes that setting, and
	// so does any third-party or hand-added key. So read the file we are
	// about to overwrite and carry its unknown keys across. See
	// preserve.go; a file that is absent, unreadable or not JSON yields
	// no residue and the write proceeds exactly as before.
	if prior, rErr := os.ReadFile(c.path); rErr == nil { //nolint:gosec // the config's own path
		if residue := captureUnknown(prior, reflect.TypeOf(v)); residue != nil {
			merged, mErr := mergeUnknown(raw, residue)
			if mErr != nil {
				log.WithError(mErr).Warn("Could not preserve unrecognized config keys; writing known fields only.")
			} else {
				raw = merged
			}
		}
	}
	// json.MarshalIndent is defined as Marshal followed by this, so the
	// on-disk formatting is byte-for-byte what it has always been.
	var indented bytes.Buffer
	if err = json.Indent(&indented, raw, "", "\t"); err != nil {
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
	return os.WriteFile(c.path, indented.Bytes(), filePerm)
}
