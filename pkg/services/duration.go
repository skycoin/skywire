// Package services pkg/services/duration.go
//
// Duration is a time.Duration wrapper that JSON-marshals as a Go
// duration string ("2m", "30s", "100ms") and unmarshals from either
// a string or a number (nanoseconds). Standard time.Duration's
// JSON support is asymmetric — it marshals as a number but won't
// unmarshal a string, so config files using human-readable
// durations fail to parse without a wrapper like this.
//
// Mirrored from pkg/visor/visorconfig.Duration so the deployment
// services can use it without importing the whole visorconfig
// package (which would broaden their dependency surface for one
// type).
package services

import (
	"encoding/json"
	"errors"
	"time"
)

// Duration is a JSON-friendly wrapper around time.Duration.
type Duration time.Duration

// MarshalJSON renders as a Go-style duration string ("2m").
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON accepts either a duration string ("2m", "5s") or
// a raw number (nanoseconds, matching time.Duration's
// MarshalJSON default).
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
		return errors.New("services: invalid duration")
	}
}

// Std returns the wrapped value as the standard time.Duration so
// callers don't have to repeat the type assertion.
func (d Duration) Std() time.Duration { return time.Duration(d) }
