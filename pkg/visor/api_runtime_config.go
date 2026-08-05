// Package visor pkg/visor/api_runtime_config.go c3-vis-core
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
	"regexp"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// topLevelSKRe matches the entire top-level "sk" line in a 2-space-indented
// MarshalIndent(v.conf) rendering (top-level fields sit at exactly two spaces;
// any nested "sk" is deeper-indented and left alone). Used to drop the secret
// key from the runtime-config view — removing the line (rather than blanking to
// "") keeps the JSON decodable, since an empty string is not a valid SecKey and
// an absent one is simply Null.
var topLevelSKRe = regexp.MustCompile(`(?m)^  "sk": "[0-9a-fA-F]*",?\n`)

// redactTopLevelSK removes the visor's secret key from a marshaled config so it
// never leaves the process via the runtime-config view. The PK is kept.
func redactTopLevelSK(b []byte) []byte {
	return topLevelSKRe.ReplaceAll(b, nil)
}

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

	// Identity is NOT editable through the runtime-config view. The PK is derived
	// from the SK, and the SK is redacted out of GetRuntimeConfig, so an honest
	// edit round-trips with the same PK and a blank SK. Reject anything that would
	// change the identity: an edited PK, or a freshly-typed SK deriving a different
	// PK. This prevents a typo (or the redaction) from silently swapping identity.
	if !newConf.PK.Null() && !v.conf.PK.Null() && newConf.PK != v.conf.PK {
		return errors.New("public key is not editable via runtime config")
	}

	// Preserve the running SK when the editor submitted a blank one (the common
	// case, since the SK is redacted). Re-marshal below so the real key — not the
	// redacted blank — lands on disk. A deliberately-typed SK is honoured but must
	// still derive the current PK (checked next).
	preservedSK := false
	if newConf.SK.Null() && !v.conf.SK.Null() {
		newConf.SK = v.conf.SK
		if newConf.PK.Null() {
			newConf.PK = v.conf.PK
		}
		preservedSK = true
	}

	// SK/PK consistency: whatever SK ends up configured must derive the current
	// public key (identity is immutable) and match the PK field when both present.
	if !newConf.SK.Null() {
		derivedPK, err := newConf.SK.PubKey()
		if err != nil {
			return fmt.Errorf("invalid sk: %w", err)
		}
		if !v.conf.PK.Null() && derivedPK != v.conf.PK {
			return errors.New("public key is not editable via runtime config")
		}
		if !newConf.PK.Null() && derivedPK != newConf.PK {
			return errors.New("pk does not match sk")
		}
	} else if !newConf.PK.Null() {
		return errors.New("pk is set but sk is empty; one without the other will fail at startup")
	}

	// Write the operator's bytes verbatim to preserve their formatting — EXCEPT
	// when we re-injected the preserved SK, where we re-marshal so the real key
	// (not the redacted blank the editor round-tripped) is what persists.
	toWrite := rawJSON
	if preservedSK {
		b, mErr := json.MarshalIndent(&newConf, "", "  ")
		if mErr != nil {
			return fmt.Errorf("re-marshal config: %w", mErr)
		}
		toWrite = b
	}
	if err := os.WriteFile(path, toWrite, 0o644); err != nil { //nolint:gosec
		return fmt.Errorf("write config to %s: %w", path, err)
	}

	v.MasterLogger().PackageLogger("visor:runtime-config").
		WithField("path", path).
		Info("Runtime config replaced via API; visor restart required for changes to take effect.")
	return nil
}
