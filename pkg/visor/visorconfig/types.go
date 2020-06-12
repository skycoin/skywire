package visorconfig

import (
	"encoding/json"
	"errors"
	"time"
)

// LogStore types.
const (
	FileLogStore   = "file"
	MemoryLogStore = "memory"
)

const (
	// DefaultTimeout is used for default config generation and if it is not set in config.
	DefaultTimeout = Duration(10 * time.Second)
)

// Duration wraps around time.Duration to allow parsing from and to JSON
type Duration time.Duration

// MarshalJSON implements json marshaling
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON implements unmarshal from json
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
