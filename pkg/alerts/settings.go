// Package alerts sends mail and webhook alerts when watched visors go
// offline, when they come back, and when their storage starts to fail.
package alerts

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Offline delay bounds. A visor that comes back within the delay sends
// nothing.
const (
	MinOfflineAfter     = time.Minute
	MaxOfflineAfter     = time.Hour
	DefaultOfflineAfter = 5 * time.Minute
)

// DefaultDiskWarn is the disk use percent that raises a storage warning.
const DefaultDiskWarn = 90

// Duration is a time.Duration written as a string such as "5m" in JSON.
type Duration time.Duration

// MarshalJSON implements json.Marshaler.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON accepts "5m" or a number of nanoseconds.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		var n int64
		if err := json.Unmarshal(b, &n); err != nil {
			return fmt.Errorf("duration: %w", err)
		}
		*d = Duration(n)
		return nil
	}
	if s == "" {
		*d = 0
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// SMTP is an optional mail relay. Without one, mail goes straight to each
// recipient domain's MX on port 25.
type SMTP struct {
	Host     string `json:"host"`
	Port     int    `json:"port,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	StartTLS bool   `json:"starttls,omitempty"`
	// PasswordSet is only set in reads, which never carry the password.
	PasswordSet bool `json:"password_set,omitempty"`
}

// Settings is the alerts block of the visor config.
type Settings struct {
	EmailTo      []string        `json:"email_to,omitempty"`
	OfflineAfter Duration        `json:"offline_after,omitempty"`
	SMTP         *SMTP           `json:"smtp,omitempty"`
	Webhook      string          `json:"webhook,omitempty"`
	Watch        []cipher.PubKey `json:"watch,omitempty"`
	// From is the sender address. Default: skywire-alerts@<hostname>.
	From string `json:"from,omitempty"`
	// DiskWarnPercent is the disk use that raises a storage warning.
	// Default: DefaultDiskWarn.
	DiskWarnPercent float64 `json:"disk_warn_percent,omitempty"`
}

// DiskWarn is the disk warning threshold, the default when unset.
func (s *Settings) DiskWarn() float64 {
	if s == nil || s.DiskWarnPercent == 0 {
		return DefaultDiskWarn
	}
	return s.DiskWarnPercent
}

// Enabled reports whether there is anywhere to send an alert.
func (s *Settings) Enabled() bool {
	return s != nil && (len(s.EmailTo) > 0 || s.Webhook != "")
}

// Delay is the offline delay, the default when unset.
func (s *Settings) Delay() time.Duration {
	if s == nil || s.OfflineAfter == 0 {
		return DefaultOfflineAfter
	}
	return time.Duration(s.OfflineAfter)
}

// Validate checks addresses, the delay range and the webhook URL.
func (s *Settings) Validate() error {
	if s == nil {
		return nil
	}
	var errs []error
	for _, a := range s.EmailTo {
		if _, err := mail.ParseAddress(a); err != nil || !strings.Contains(a, "@") {
			errs = append(errs, fmt.Errorf("invalid email address %q", a))
		}
	}
	if d := time.Duration(s.OfflineAfter); d != 0 && (d < MinOfflineAfter || d > MaxOfflineAfter) {
		errs = append(errs, fmt.Errorf("offline_after %s is outside %s to %s", d, MinOfflineAfter, MaxOfflineAfter))
	}
	if s.DiskWarnPercent < 0 || s.DiskWarnPercent > 100 {
		errs = append(errs, fmt.Errorf("disk_warn_percent %.0f is outside 0 to 100", s.DiskWarnPercent))
	}
	if s.Webhook != "" {
		u, err := url.Parse(s.Webhook)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("webhook %q is not an http or https URL", s.Webhook))
		}
	}
	if s.SMTP != nil {
		if s.SMTP.Host == "" {
			errs = append(errs, errors.New("smtp relay needs a host"))
		}
		if s.SMTP.Port < 0 || s.SMTP.Port > 65535 {
			errs = append(errs, fmt.Errorf("invalid smtp port %d", s.SMTP.Port))
		}
	}
	if s.From != "" {
		if _, err := mail.ParseAddress(s.From); err != nil {
			errs = append(errs, fmt.Errorf("invalid sender address %q", s.From))
		}
	}
	return errors.Join(errs...)
}

// Redacted is a copy without the relay password.
func (s Settings) Redacted() Settings {
	if s.SMTP != nil {
		r := *s.SMTP
		r.PasswordSet = r.Password != ""
		r.Password = ""
		s.SMTP = &r
	}
	return s
}

// Merge returns next, keeping the current relay password when next names
// the same relay and leaves the password empty.
func Merge(cur *Settings, next Settings) Settings {
	if next.SMTP != nil {
		r := *next.SMTP
		r.PasswordSet = false
		if r.Password == "" && cur != nil && cur.SMTP != nil && cur.SMTP.Host == r.Host {
			r.Password = cur.SMTP.Password
		}
		next.SMTP = &r
	}
	return next
}
