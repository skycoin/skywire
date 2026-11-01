// Package visor pkg/visor/skymail.go c3-vis-core
//
// The visor's side of the skymail app (cmd/apps/skymail). The app is the
// mailbox: it serves port 25 over dmsg and skynet through the app client,
// like any app. The visor keeps what an app cannot have: its secret key,
// which seals and opens envelopes (MailHost), and its hypervisor relation,
// the default helpers. The visor's mail API is a client of the app's
// control surface, in process or over TCP.
package visor

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// skymailApp is the mailbox's name in the launcher.
const skymailApp = "skymail"

// errMailboxOff answers every mail call while the mailbox is not running.
var errMailboxOff = errors.New("mailbox is not running on this visor")

// errHostMTA is why the mailbox stays down when `serve add 25` hands port
// 25 to the host's mail server, which then receives the mail instead.
var errHostMTA = errors.New("port 25 is served by the host's mail server (`serve add 25`)")

// skymailEnabled: on unless the config section turns it off.
func skymailEnabled(conf *visorconfig.V1) bool {
	if conf != nil && conf.Skymail != nil {
		return conf.Skymail.Enable
	}
	return true
}

// skymailDir is the app's --dir when the config lists the app, else the
// config section's, else "mail" beside the local path, not inside it: the
// wasm visor never persists local/, and mail must survive a reload.
func (v *Visor) skymailDir() string {
	if d := readAppArg(v, skymailApp, "--dir"); d != "" {
		return d
	}
	return skymailDir(v.conf)
}

func skymailDir(conf *visorconfig.V1) string {
	if conf.Skymail != nil && conf.Skymail.Dir != "" {
		return conf.Skymail.Dir
	}
	return filepath.Join(filepath.Dir(filepath.Clean(conf.LocalPath)), "mail")
}

func skymailLimits(conf *visorconfig.V1) skymail.Limits {
	if conf == nil || conf.Skymail == nil {
		return skymail.Limits{}
	}
	s := conf.Skymail
	return skymail.Limits{
		MaxMessageSize: s.MaxMessageSize,
		MaxTotalSize:   s.MaxTotalSize,
		MaxAge:         time.Duration(s.MaxAge),
	}
}

// skymailEffective is whether the mailbox should run and its limits:
// settings.json, then the config section, then the defaults.
func skymailEffective(conf *visorconfig.V1) (bool, skymail.Limits, error) {
	return skymailEffectiveIn(conf, skymailDir(conf))
}

func skymailEffectiveIn(conf *visorconfig.V1, dir string) (bool, skymail.Limits, error) {
	enabled := skymailEnabled(conf)
	s, err := skymail.LoadSettings(dir)
	if s.Enable != nil {
		enabled = *s.Enable
	}
	return enabled, s.Over(skymailLimits(conf)), err
}

// skymailArgs start the app with the config section as its baseline. The
// directory is absolute, as an external app runs in a directory of its own.
func skymailArgs(conf *visorconfig.V1) []string {
	dir := skymailDir(conf)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	args := []string{"--dir", dir}
	l := skymailLimits(conf)
	size := func(flag string, n int64) {
		switch {
		case n > 0:
			args = append(args, flag, strconv.FormatInt(n, 10))
		case n < 0:
			args = append(args, flag, "none")
		}
	}
	size("--max-message-size", l.MaxMessageSize)
	size("--max-total-size", l.MaxTotalSize)
	switch {
	case l.MaxAge > 0:
		args = append(args, "--max-age", l.MaxAge.String())
	case l.MaxAge < 0:
		args = append(args, "--max-age", "none")
	}
	return args
}

// mailHost is the visor's side of the skymail app's gateway calls.
type mailHost struct{ v *Visor }

func (h mailHost) MailSeal(msg []byte, rcpt cipher.PubKey) ([]byte, error) {
	return skymail.Seal(msg, h.v.conf.SK, rcpt)
}

func (h mailHost) MailOpen(body []byte) ([]byte, cipher.PubKey, error) {
	return skymail.OpenSealed(body, h.v.conf.SK)
}

func (h mailHost) MailHostInfo() appserver.MailHostInfo {
	return appserver.MailHostInfo{
		Managed:     h.v.managedVisorPKs(),
		Hypervisors: h.v.configuredHypervisors(),
		PortServed:  h.v.mailPortServed(),
	}
}

// appMailHost serves the gateway's mail calls to the skymail app only. Any
// app can already send mail as this visor, since the transport
// authenticates the visor's key, but only the mailbox may open its mail.
func (v *Visor) appMailHost(appName string) appserver.MailHost {
	if appName != skymailApp || v.conf == nil {
		return nil
	}
	return mailHost{v}
}

func (v *Visor) mailPortServed() bool {
	return v.forwardedPorts != nil && v.forwardedPorts.Get(skymail.Port) != nil
}

// managedVisorPKs are the visors connected to this visor's hypervisor.
func (v *Visor) managedVisorPKs() []cipher.PubKey {
	hv := v.hvInstance
	if hv == nil {
		return nil
	}
	hv.mu.RLock()
	defer hv.mu.RUnlock()
	out := make([]cipher.PubKey, 0, len(hv.remoteVisors))
	for pk := range hv.remoteVisors {
		out = append(out, pk)
	}
	return out
}

// mailControl reaches the running app's control surface: the handler an
// in-process app publishes, or the TCP address the config gives an
// external one, with the token it wrote beside the mail.
func (v *Visor) mailControl() (skymail.Control, error) {
	h := v.mailHandler
	if h == nil {
		h = launcher.GetHTTPHandler(skymailApp)
	}
	if h != nil {
		return skymail.Control{Do: func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			return rec.Result(), nil
		}}, nil
	}
	addr := readAppArg(v, skymailApp, "--addr")
	if addr == "" || !v.mailAppRunning() {
		return skymail.Control{}, errMailboxOff
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	dir := v.skymailDir()
	client := &http.Client{}
	return skymail.Control{Do: func(req *http.Request) (*http.Response, error) {
		token, err := os.ReadFile(filepath.Join(dir, skymail.TokenFile)) //nolint:gosec // our own file
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errMailboxOff, err)
		}
		req.URL.Scheme, req.URL.Host = "http", addr
		req.Header.Set(skymail.TokenHeader, string(bytes.TrimSpace(token)))
		return client.Do(req) //nolint:gosec // the visor's own configured address
	}}, nil
}

func (v *Visor) mailAppState() (*appserver.AppState, bool) {
	if v.appL == nil {
		return nil, false
	}
	return v.appL.AppState(skymailApp)
}

func (v *Visor) mailAppRunning() bool {
	st, ok := v.mailAppState()
	return ok && st.Status == appserver.AppStatusRunning
}

// MailStatus implements visorapi.API.
func (v *Visor) MailStatus() (*visorapi.MailStatus, error) {
	v.initLock.Lock()
	enabled, limits, _ := skymailEffectiveIn(v.conf, v.skymailDir()) //nolint:errcheck // a status line
	v.initLock.Unlock()
	st := &visorapi.MailStatus{Enabled: enabled, Limits: limits.WithDefaults(), Whitelist: []cipher.PubKey{}}
	c, err := v.mailControl()
	var s skymail.Status
	if err == nil {
		s, err = c.Status()
	}
	if err != nil {
		st.Reason = v.mailOffReason(enabled)
		return st, nil
	}
	st.Running = true
	st.Address, st.AddressDmsg, st.Dir = s.Address, s.AddressDmsg, s.Dir
	st.Limits, st.Usage = s.Limits, s.Usage
	if s.Whitelist != nil {
		st.Whitelist = s.Whitelist
	}
	st.Unread, st.Total = s.Unread, s.Total
	st.HasOutbox, st.Outbox = s.HasOutbox, s.Outbox
	st.Helpers, st.HelpersDefault = s.Helpers, s.HelpersDefault
	st.RelayFor, st.RelayForDefault = s.RelayFor, s.RelayForDefault
	return st, nil
}

// mailOffReason says why the mailbox is not running.
func (v *Visor) mailOffReason(enabled bool) string {
	switch {
	case !enabled:
		return "disabled"
	case v.mailPortServed():
		return errHostMTA.Error()
	}
	if st, ok := v.mailAppState(); ok {
		if st.DetailedStatus != "" {
			return st.DetailedStatus
		}
		return st.Status.String()
	}
	return "not started"
}

// MailSetSettings implements visorapi.API: it is kept in the mailbox's
// settings.json, which survives a restart and, in a browser tab, a
// reload, and applies at once.
func (v *Visor) MailSetSettings(u visorapi.MailSettingsUpdate) error {
	v.initLock.Lock()
	dir := v.skymailDir()
	v.initLock.Unlock()
	s, err := skymail.LoadSettings(dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", skymail.SettingsFile, err)
	}
	if u.Enable != nil {
		s.Enable = u.Enable
	}
	if u.MaxMessageSize != nil {
		s.MaxMessageSize = u.MaxMessageSize
	}
	if u.MaxTotalSize != nil {
		s.MaxTotalSize = u.MaxTotalSize
	}
	if u.MaxAge != nil {
		a := skymail.Age(*u.MaxAge)
		s.MaxAge = &a
	}
	switch {
	case u.HelpersDefault:
		s.Helpers = nil
	case u.Helpers != nil:
		h := append([]cipher.PubKey{}, *u.Helpers...)
		s.Helpers = &h
	}
	switch {
	case u.RelayForDefault:
		s.RelayFor = nil
	case u.RelayFor != nil:
		r := append([]cipher.PubKey{}, *u.RelayFor...)
		s.RelayFor = &r
	}
	if err := s.Save(dir); err != nil {
		return fmt.Errorf("save %s: %w", skymail.SettingsFile, err)
	}

	v.initLock.Lock()
	enable, _, _ := skymailEffectiveIn(v.conf, dir) //nolint:errcheck // just written
	v.initLock.Unlock()
	if !enable {
		if v.mailAppRunning() {
			return v.StopApp(skymailApp)
		}
		return nil
	}
	if c, err := v.mailControl(); err == nil {
		return c.Reload()
	}
	if v.appL == nil {
		return errMailboxOff
	}
	return v.StartApp(skymailApp)
}

func (v *Visor) mailCall(f func(skymail.Control) error) error {
	c, err := v.mailControl()
	if err != nil {
		return err
	}
	return f(c)
}

// MailList implements visorapi.API.
func (v *Visor) MailList(folder string) (list []skymail.Summary, err error) {
	return list, v.mailCall(func(c skymail.Control) error {
		list, err = c.List(folder)
		return err
	})
}

// MailRead implements visorapi.API.
func (v *Visor) MailRead(folder, id string) (m *skymail.Rendered, err error) {
	return m, v.mailCall(func(c skymail.Control) error {
		m, err = c.Read(folder, id)
		return err
	})
}

// MailRaw implements visorapi.API.
func (v *Visor) MailRaw(folder, id string) (raw []byte, err error) {
	return raw, v.mailCall(func(c skymail.Control) error {
		raw, err = c.Raw(folder, id)
		return err
	})
}

// MailAttachment implements visorapi.API.
func (v *Visor) MailAttachment(folder, id string, n int) (a *skymail.AttachmentData, err error) {
	return a, v.mailCall(func(c skymail.Control) error {
		a, err = c.Attachment(folder, id, n)
		return err
	})
}

// MailSend implements visorapi.API.
func (v *Visor) MailSend(msg skymail.Outgoing) (res *skymail.SendResult, err error) {
	return res, v.mailCall(func(c skymail.Control) error {
		res, err = c.Send(msg)
		return err
	})
}

// MailDelete implements visorapi.API.
func (v *Visor) MailDelete(folder, id string) error {
	return v.mailCall(func(c skymail.Control) error { return c.Delete(folder, id) })
}

// MailRetry implements visorapi.API.
func (v *Visor) MailRetry(id string) error {
	return v.mailCall(func(c skymail.Control) error { return c.Retry(id) })
}

// MailSetWhitelist implements visorapi.API.
func (v *Visor) MailSetWhitelist(pks []cipher.PubKey) error {
	return v.mailCall(func(c skymail.Control) error { return c.SetWhitelist(pks) })
}
