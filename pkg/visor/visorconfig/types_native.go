//go:build !js

// Package visorconfig pkg/visor/visorconfig/types_native.go
//
// Duration's JSON codec methods. Tagged off the WASM build because
// json.Marshal / json.Unmarshal pull the reflect runtime helpers
// TinyGo's stdlib doesn't provide. See types.go's package doc for
// the broader rationale.
package visorconfig

import (
	"encoding/json"
	"errors"
	"time"
)

// MarshalJSON implements json marshaling.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON implements unmarshal from json.
func (d *Duration) UnmarshalJSON(b []byte) error {
	if len(b) == 0 {
		*d = 0
		return nil
	}

	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch value := v.(type) {
	case float64:
		*d = Duration(time.Duration(value))
		return nil
	case string:
		tmp, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		*d = Duration(tmp)
		return nil
	default:
		return errors.New("invalid duration")
	}
}
