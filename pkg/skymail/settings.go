package skymail

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// SettingsFile keeps the settings changed at runtime beside the mail, like
// the whitelist. It wins over the baseline the mailbox was started with,
// so a change survives a restart and, in a browser tab, a reload that
// regenerates the visor config.
const SettingsFile = "settings.json"

// Settings is what SettingsFile holds. A nil field is unset.
type Settings struct {
	Enable         *bool  `json:"enable,omitempty"`
	MaxMessageSize *int64 `json:"max_message_size,omitempty"`
	MaxTotalSize   *int64 `json:"max_total_size,omitempty"`
	MaxAge         *Age   `json:"max_age,omitempty"`
	Relay
}

// Relay is who carries this mailbox's queued mail and whose mail it
// carries. nil is the default, empty is none.
type Relay struct {
	Helpers  *[]cipher.PubKey `json:"helpers,omitempty"`
	RelayFor *[]cipher.PubKey `json:"relay_for,omitempty"`
}

// Age is a duration written as text, such as "168h0m0s". It also reads
// the nanosecond number the wasm visor once wrote.
type Age time.Duration

// MarshalJSON implements json.Marshaler.
func (a Age) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(a).String())
}

// UnmarshalJSON implements json.Unmarshaler.
func (a *Age) UnmarshalJSON(b []byte) error {
	var n int64
	if err := json.Unmarshal(b, &n); err == nil {
		*a = Age(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*a = Age(d)
	return nil
}

// LoadSettings reads dir's SettingsFile. A missing file is no settings.
func LoadSettings(dir string) (Settings, error) {
	var s Settings
	raw, err := os.ReadFile(filepath.Join(dir, SettingsFile)) //nolint:gosec // our own file
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(raw, &s)
}

// Save writes s to dir's SettingsFile in one step.
func (s Settings) Save(dir string) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, SettingsFile)
	if err := os.WriteFile(path+".tmp", raw, 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// Over returns base with the limits s sets in place of base's.
func (s Settings) Over(base Limits) Limits {
	if s.MaxMessageSize != nil {
		base.MaxMessageSize = *s.MaxMessageSize
	}
	if s.MaxTotalSize != nil {
		base.MaxTotalSize = *s.MaxTotalSize
	}
	if s.MaxAge != nil {
		base.MaxAge = time.Duration(*s.MaxAge)
	}
	return base
}
