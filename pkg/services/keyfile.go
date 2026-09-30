// Package services pkg/services/keyfile.go c4-net-discovery
package services

import (
	"encoding/json"
	"fmt"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
)

// withKeyFile resolves a block's "keyfile" into its "secret_key" (and
// "public_key"), the way --keyfile does for a single service, so a
// services.json can keep each service's key in its own file — a Docker
// secret, say — rather than inline. A block with an inline secret_key, or
// no keyfile, is returned unchanged.
func withKeyFile(raw json.RawMessage) (json.RawMessage, error) {
	var probe struct {
		KeyFile string        `json:"keyfile"`
		SecKey  cipher.SecKey `json:"secret_key"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}
	if probe.KeyFile == "" || !probe.SecKey.Null() {
		return raw, nil
	}
	var sk cipher.SecKey
	if err := cmdutil.LoadOrGenerateKey(probe.KeyFile, &sk); err != nil {
		return nil, fmt.Errorf("keyfile %s: %w", probe.KeyFile, err)
	}
	pk, err := sk.PubKey()
	if err != nil {
		return nil, fmt.Errorf("keyfile %s: %w", probe.KeyFile, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for k, v := range map[string]string{"secret_key": sk.Hex(), "public_key": pk.Hex()} {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		fields[k] = b
	}
	return json.Marshal(fields)
}
