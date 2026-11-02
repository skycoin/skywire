// Package visor pkg/visor/dmsg_async_settings.go
package visor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgasync"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// asyncSettingsFile keeps the dmsg-async settings changed at runtime in the
// dmsg-async directory. They win over the config's dmsg_async section, so a
// change survives a restart and, in a browser tab, a reload.
const asyncSettingsFile = "settings.json"

// asyncSettings is what asyncSettingsFile holds. A nil field is unset.
type asyncSettings struct {
	Host         *bool            `json:"host,omitempty"`
	Hosted       *[]cipher.PubKey `json:"hosted,omitempty"`
	MaxMessage   *int             `json:"max_message,omitempty"`
	MaxTotalSize *int64           `json:"max_total_size,omitempty"`
	TTL          *skymail.Age     `json:"ttl,omitempty"`
	Inbox        *bool            `json:"inbox,omitempty"`
	InboxHosts   *[]cipher.PubKey `json:"inbox_hosts,omitempty"`
	Publish      *bool            `json:"publish,omitempty"`
	Allow        *[]cipher.PubKey `json:"allow,omitempty"`
}

func loadAsyncSettings(dir string) (asyncSettings, error) {
	var s asyncSettings
	raw, err := os.ReadFile(filepath.Join(dir, asyncSettingsFile)) //nolint:gosec // our own file
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(raw, &s)
	return s, err
}

func (s asyncSettings) save(dir string) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, asyncSettingsFile)
	if err := os.WriteFile(path+".tmp", raw, 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// over returns c with the settings s sets in place of c's.
func (s asyncSettings) over(c visorconfig.DmsgAsyncConfig) visorconfig.DmsgAsyncConfig {
	if s.Host != nil {
		on := *s.Host
		c.Host.Enable = &on
	}
	if s.Hosted != nil {
		c.Host.Hosted = slices.Clone(*s.Hosted)
	}
	if s.MaxMessage != nil {
		c.Host.MaxMessage = *s.MaxMessage
	}
	if s.MaxTotalSize != nil {
		c.Host.MaxTotalSize = *s.MaxTotalSize
	}
	if s.TTL != nil {
		c.Host.TTL = visorconfig.Duration(*s.TTL)
	}
	if s.Inbox != nil {
		c.Inbox.Disable = !*s.Inbox
	}
	if s.InboxHosts != nil {
		c.Inbox.Hosts = slices.Clone(*s.InboxHosts)
	}
	if s.Publish != nil {
		on := *s.Publish
		c.Inbox.Publish = &on
	}
	if s.Allow != nil {
		c.Inbox.Allow = slices.Clone(*s.Allow)
	}
	return c
}

// keys lists the keys s sets.
func (s asyncSettings) keys() []string {
	var out []string
	add := func(set bool, k string) {
		if set {
			out = append(out, k)
		}
	}
	add(s.Host != nil, "host")
	add(s.Hosted != nil, "hosted")
	add(s.MaxMessage != nil, "max_message")
	add(s.MaxTotalSize != nil, "max_total_size")
	add(s.TTL != nil, "ttl")
	add(s.Inbox != nil, "inbox")
	add(s.InboxHosts != nil, "inbox_hosts")
	add(s.Publish != nil, "publish")
	add(s.Allow != nil, "allow")
	return out
}

// set applies one key=value. "default" unsets the key, so the config's value
// applies again.
func (s *asyncSettings) set(key, val string) error {
	def := val == "default"
	boolean := func(p **bool) error {
		if def {
			*p = nil
			return nil
		}
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("%s: want true, false or default", key)
		}
		*p = &b
		return nil
	}
	keys := func(p **[]cipher.PubKey) error {
		if def {
			*p = nil
			return nil
		}
		list := []cipher.PubKey{}
		if val != "none" && val != "" {
			for _, f := range strings.Split(val, ",") {
				var pk cipher.PubKey
				if err := pk.Set(strings.TrimSpace(f)); err != nil {
					return fmt.Errorf("%s: %q: %w", key, f, err)
				}
				list = append(list, pk)
			}
		}
		*p = &list
		return nil
	}
	switch key {
	case "host":
		return boolean(&s.Host)
	case "inbox":
		return boolean(&s.Inbox)
	case "publish":
		return boolean(&s.Publish)
	case "hosted":
		return keys(&s.Hosted)
	case "inbox_hosts":
		return keys(&s.InboxHosts)
	case "allow":
		return keys(&s.Allow)
	case "max_message":
		if def {
			s.MaxMessage = nil
			return nil
		}
		n, err := skymail.ParseSize(val)
		if err != nil || n <= 0 || n > dmsgasync.MaxMessageLimit {
			return fmt.Errorf("%s: want a size up to 1MiB, such as 64KiB", key)
		}
		m := int(n)
		s.MaxMessage = &m
	case "max_total_size":
		if def {
			s.MaxTotalSize = nil
			return nil
		}
		n, err := skymail.ParseSize(val)
		if err != nil || n <= 0 {
			return fmt.Errorf("%s: want a size, such as 256MiB", key)
		}
		s.MaxTotalSize = &n
	case "ttl":
		if def {
			s.TTL = nil
			return nil
		}
		d, err := skymail.ParseAge(val)
		if err != nil || d <= 0 {
			return fmt.Errorf("%s: want a duration, such as 7d or 36h", key)
		}
		a := skymail.Age(d)
		s.TTL = &a
	default:
		return fmt.Errorf("unknown key %q", key)
	}
	return nil
}

// asyncSettingsView is the effective configuration conf as visorapi shows it.
func (v *Visor) asyncSettingsView(conf visorconfig.DmsgAsyncConfig, set []string) *visorapi.DmsgAsyncSettings {
	lim := dmsgasync.Limits{
		MaxMessage: conf.Host.MaxMessage,
		MaxTotal:   conf.Host.MaxTotalSize,
		TTL:        time.Duration(conf.Host.TTL),
	}.WithDefaults()
	return &visorapi.DmsgAsyncSettings{
		Host:         asyncHostOn(conf, v.conf),
		Hosted:       conf.Host.Hosted,
		MaxMessage:   lim.MaxMessage,
		MaxTotalSize: lim.MaxTotal,
		TTL:          lim.TTL,
		Inbox:        !conf.Inbox.Disable,
		InboxHosts:   asyncHosts(conf, v.conf),
		Publish:      conf.Inbox.Publish == nil || *conf.Inbox.Publish,
		Allow:        conf.Inbox.Allow,
		Set:          set,
	}
}

// DmsgAsyncSettings implements visorapi.API.
func (v *Visor) DmsgAsyncSettings() (*visorapi.DmsgAsyncSettings, error) {
	if v.dmsgC == nil {
		return nil, ErrDmsgAsyncOff
	}
	conf, s := v.asyncConfig()
	return v.asyncSettingsView(conf, s.keys()), nil
}

// DmsgAsyncSetSettings implements visorapi.API. It restarts only what the
// change touches: the host for host keys, the inbox loop for inbox keys.
func (v *Visor) DmsgAsyncSetSettings(kv map[string]string) error {
	if v.dmsgC == nil {
		return ErrDmsgAsyncOff
	}
	a := &v.async
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	dir := asyncDir(v.conf)
	s, err := loadAsyncSettings(dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", asyncSettingsFile, err)
	}
	for k, val := range kv {
		if err := s.set(k, val); err != nil {
			return err
		}
	}
	if err := s.save(dir); err != nil {
		return fmt.Errorf("write %s: %w", asyncSettingsFile, err)
	}
	conf, _ := v.asyncConfig()
	a.mu.Lock()
	old := a.conf
	a.mu.Unlock()
	v.asyncApply(conf, !reflect.DeepEqual(old.Host, conf.Host), !reflect.DeepEqual(old.Inbox, conf.Inbox))
	return nil
}
