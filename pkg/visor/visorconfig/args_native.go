//go:build !js

// Package visorconfig — args_native.go
//
// encoding/json-using halves of args.go. Build-tag-gated off the
// WASM path because encoding/json's reflect-based marshaller drags
// reflect.unsafe_New / mapassign / typedmemmove (which TinyGo's
// stdlib doesn't ship). The browser-side WASM never (de)serializes
// V1 apps lists; under js V1.Apps stays as raw appsList values and
// the type's json methods are absent from the build.
package visorconfig

import (
	"encoding/json"
	"fmt"

	appspec "github.com/skycoin/skywire/pkg/app/appserver/spec"
)

// MarshalJSON emits each AppConfig's Args as a shell-quoted string.
func (a appsList) MarshalJSON() ([]byte, error) {
	out := make([]appConfigOnDisk, len(a))
	for i, c := range a {
		out[i] = toOnDisk(c)
	}
	return json.Marshal(out)
}

// UnmarshalJSON accepts both the new string form and the legacy
// array form on a per-app basis. Mixed configs work too.
func (a *appsList) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := make([]appspec.AppConfig, len(raw))
	for i, r := range raw {
		cfg, err := unmarshalAppConfig(r)
		if err != nil {
			return fmt.Errorf("apps[%d]: %w", i, err)
		}
		out[i] = cfg
	}
	*a = out
	return nil
}

func unmarshalAppConfig(raw json.RawMessage) (appspec.AppConfig, error) {
	// Shadow AppConfig.Args with a RawMessage at the outer scope so
	// we can dispatch on its JSON shape (string vs array).
	var pre struct {
		appspec.AppConfig
		Args json.RawMessage `json:"args,omitempty"`
	}
	if err := json.Unmarshal(raw, &pre); err != nil {
		return appspec.AppConfig{}, err
	}
	cfg := pre.AppConfig
	if len(pre.Args) == 0 {
		cfg.Args = nil
		return cfg, nil
	}
	// Prefer the new string form; fall back to the array form for
	// existing on-disk configs.
	var s string
	if err := json.Unmarshal(pre.Args, &s); err == nil {
		parsed, perr := splitArgs(s)
		if perr != nil {
			return appspec.AppConfig{}, fmt.Errorf("parsing args string: %w", perr)
		}
		cfg.Args = parsed
		return cfg, nil
	}
	if err := json.Unmarshal(pre.Args, &cfg.Args); err != nil {
		return appspec.AppConfig{}, fmt.Errorf("args must be string or array: %w", err)
	}
	return cfg, nil
}
