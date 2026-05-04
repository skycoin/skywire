// Package visor pkg/visor/api_runtime_config.go
//
// Whole-config replacement for the visor's on-disk config file.
// Companion to GetRuntimeConfig: lets the hvui round-trip the
// config through an editor pane and write the user-edited JSON
// back to disk.
//
// Validation chain — failure at any step rejects the write:
//  1. Strict JSON decode into a fresh visorconfig.V1 with
//     DisallowUnknownFields(), so typos in field names surface
//     instead of silently dropping data.
//  2. SK/PK consistency — when both are present the PK must
//     derive from the SK; when only SK is present the visor
//     identity isn't accidentally rotated by an empty PK.
//  3. Non-empty config path on the live visor (refuse on STDIN-
//     sourced or in-memory configs).
//
// We deliberately do NOT attempt a hot-reload: the visor's many
// subsystems (router, app launcher, dmsg client, etc.) hold
// references to v.conf at startup, so swapping it in-process
// would leave them stale. Caller must restart the visor for the
// change to take effect — the hvui surfaces this in the response.
package visor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// SetRuntimeConfig validates and writes the visor's on-disk config.
// The visor is NOT hot-reloaded; the operator must restart the
// process for the new config to take effect.
func (v *Visor) SetRuntimeConfig(rawJSON []byte) error {
	if v.conf == nil {
		return errors.New("visor has no current config")
	}
	path := v.conf.Path()
	if path == "" {
		return errors.New("visor config is not file-backed (STDIN or in-memory); refusing to write")
	}

	// Strict decode: catch typos in field names that would
	// silently drop data on the next visor start.
	var newConf visorconfig.V1
	dec := json.NewDecoder(bytes.NewReader(rawJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&newConf); err != nil {
		return fmt.Errorf("invalid runtime config json: %w", err)
	}

	// SK/PK consistency. ensureKeys is unexported on visorconfig
	// so we replicate the check here. Either field may be empty
	// (the visor will regenerate on startup), but if both are
	// present the PK must match what SK derives.
	if !newConf.SK.Null() {
		derivedPK, err := newConf.SK.PubKey()
		if err != nil {
			return fmt.Errorf("invalid sk: %w", err)
		}
		if !newConf.PK.Null() && derivedPK != newConf.PK {
			return errors.New("pk does not match sk")
		}
	} else if !newConf.PK.Null() {
		return errors.New("pk is set but sk is empty; one without the other will fail at startup")
	}

	// Write the user's bytes verbatim — preserves the formatting
	// they typed in the editor. The standard Flush() codepath
	// re-marshals from the in-memory struct which would clobber
	// any whitespace / comment-style additions the operator made.
	if err := os.WriteFile(path, rawJSON, 0o644); err != nil { //nolint:gosec
		return fmt.Errorf("write config to %s: %w", path, err)
	}

	v.MasterLogger().PackageLogger("visor:runtime-config").
		WithField("path", path).
		Info("Runtime config replaced via API; visor restart required for changes to take effect.")
	return nil
}
