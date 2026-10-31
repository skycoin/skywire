// Package visor pkg/visor/skymail.go c3-vis-core
//
// The visor's own mailbox (pkg/skymail). It listens for SMTP on port 25
// over dmsg and skynet, like every dual-listened visor port, and sends
// by dialing the recipient visor's port 25 directly: no MTA and no
// certificate, so it runs as-is in a browser tab. Mail for a visor that
// is offline waits in the Outbox and is retried from this visor. It is on by
// default everywhere; the size and age limits keep an open mailbox from
// filling the storage it lives in. Enable and every limit are live
// settings (MailSetSettings), kept in settings.json beside the mail.
package visor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/skymailbridge"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// skymailPort is SMTP's port, on dmsg and skynet alike.
const skymailPort = 25

// errMailboxOff answers every mail call while the mailbox is not running.
var errMailboxOff = errors.New("mailbox is not running on this visor")

// skymailHost owns the mailbox's lifecycle on a visor. Its zero value is
// a mailbox that has not been started.
type skymailHost struct {
	mu     sync.Mutex
	ctx    context.Context // the visor's, captured at init
	log    *logging.Logger
	rt     *skymailRuntime
	reason string // why rt is nil
}

type skymailRuntime struct {
	mb     *skymail.Mailbox
	dir    string
	cancel context.CancelFunc
	// relay holds the explicit helper and relay-for lists, nil for the
	// default. Settings swap it in place.
	relay atomic.Pointer[skymailRelay]

	lisMu sync.Mutex
	lis   []net.Listener
}

func (rt *skymailRuntime) track(l net.Listener) {
	rt.lisMu.Lock()
	rt.lis = append(rt.lis, l)
	rt.lisMu.Unlock()
}

// stop closes the listeners before returning, so port 25 can be bound
// again straight away.
func (rt *skymailRuntime) stop() {
	rt.cancel()
	rt.lisMu.Lock()
	defer rt.lisMu.Unlock()
	for _, l := range rt.lis {
		_ = l.Close() //nolint:errcheck
	}
	rt.lis = nil
}

// skymailEnabled: on unless the config section turns it off.
func skymailEnabled(conf *visorconfig.V1) bool {
	if conf != nil && conf.Skymail != nil {
		return conf.Skymail.Enable
	}
	return true
}

// skymailDir defaults to "mail" beside the local path, not inside it:
// the wasm visor never persists local/, and mail must survive a reload.
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

// skymailSettingsFile keeps the settings changed at runtime beside the
// mail, like the whitelist. The config cannot hold them: the wasm desk
// regenerates the visor config on every boot, so a flushed change was
// gone after a tab reload. Order of precedence: this file, the config
// section, the defaults.
const skymailSettingsFile = "settings.json"

type skymailSaved struct {
	Enable         *bool                 `json:"enable,omitempty"`
	MaxMessageSize *int64                `json:"max_message_size,omitempty"`
	MaxTotalSize   *int64                `json:"max_total_size,omitempty"`
	MaxAge         *visorconfig.Duration `json:"max_age,omitempty"`
	skymailRelay
}

// skymailRelay is who carries this visor's queued mail and whose mail it
// carries. nil is the default, empty is none.
type skymailRelay struct {
	Helpers  *[]cipher.PubKey `json:"helpers,omitempty"`
	RelayFor *[]cipher.PubKey `json:"relay_for,omitempty"`
}

// mailHelpers is who gets this visor's queued mail: the explicit list,
// or the visors it manages as a hypervisor, which trust it already.
func (v *Visor) mailHelpers(rt *skymailRuntime) ([]cipher.PubKey, bool) {
	if r := rt.relay.Load(); r != nil && r.Helpers != nil {
		return *r.Helpers, false
	}
	return v.managedVisorPKs(), true
}

// mailRelayFor is whose sealed mail this visor carries: the explicit
// list, or its hypervisors, which can already do anything on it.
func (v *Visor) mailRelayFor(rt *skymailRuntime) ([]cipher.PubKey, bool) {
	if r := rt.relay.Load(); r != nil && r.RelayFor != nil {
		return *r.RelayFor, false
	}
	return v.configuredHypervisors(), true
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

func loadSkymailSaved(dir string) (skymailSaved, error) {
	var s skymailSaved
	raw, err := os.ReadFile(filepath.Join(dir, skymailSettingsFile)) //nolint:gosec // our own file
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(raw, &s)
}

func (s skymailSaved) save(dir string) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, skymailSettingsFile)
	if err := os.WriteFile(path+".tmp", raw, 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// skymailEffective is whether the mailbox should run and its limits.
func skymailEffective(conf *visorconfig.V1) (bool, skymail.Limits, error) {
	enabled, l := skymailEnabled(conf), skymailLimits(conf)
	s, err := loadSkymailSaved(skymailDir(conf))
	if s.Enable != nil {
		enabled = *s.Enable
	}
	if s.MaxMessageSize != nil {
		l.MaxMessageSize = *s.MaxMessageSize
	}
	if s.MaxTotalSize != nil {
		l.MaxTotalSize = *s.MaxTotalSize
	}
	if s.MaxAge != nil {
		l.MaxAge = time.Duration(*s.MaxAge)
	}
	return enabled, l, err
}

// skymailApp is the mailbox's name in the launcher: `cli visor app ls`, start,
// stop and autostart treat it like any other app. It runs in the visor process,
// as pty and the resolving proxies do, so it works the same in a browser tab.
const skymailApp = "skymail"

// errHostMTA is why the mailbox stays down when `serve add 25` hands skynet
// port 25 to the host's mail server, which then receives the mail instead.
var errHostMTA = errors.New("port 25 is served by the host's mail server (`serve add 25`)")

func initSkymail(ctx context.Context, v *Visor, log *logging.Logger) error {
	v.mail.mu.Lock()
	v.mail.ctx, v.mail.log = ctx, log
	v.mail.reason = "not started"
	v.mail.mu.Unlock()
	if v.conf == nil {
		return nil
	}
	if _, _, err := skymailEffective(v.conf); err != nil {
		log.WithError(err).Warn("mailbox: unreadable " + skymailSettingsFile + "; using the config")
	}
	launcher.RegisterApp(skymailApp, v.skymailAppFunc())
	return nil
}

// skymailAppFunc runs the mailbox for as long as the app runs.
func (v *Visor) skymailAppFunc() appcommon.AppFunc {
	return func(ctx context.Context, _ []string) error {
		appCl := app.NewClient(nil)
		defer appCl.Close()
		appCl.SetStatusOrLog(appserver.AppDetailedStatusStarting)
		if v.dmsgC != nil {
			select {
			case <-v.dmsgC.Ready():
			case <-ctx.Done():
				return nil
			}
		}
		if err := v.startSkymail(); err != nil {
			// The host's own mail server owning port 25 is a setup, not a fault.
			if errors.Is(err, errHostMTA) {
				appCl.SetStatusOrLog(appserver.AppDetailedStatus(err.Error()))
				return nil
			}
			appCl.SetErrorOrLog(err)
			return err
		}
		appCl.SetStatusOrLog(appserver.AppDetailedStatusRunning)
		<-ctx.Done()
		v.stopSkymail("stopped")
		appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)
		return nil
	}
}

// startSkymail brings the mailbox up; it is a no-op when it runs.
func (v *Visor) startSkymail() error {
	h := &v.mail
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rt != nil {
		return nil
	}
	fail := func(err error) error {
		h.reason = err.Error()
		return err
	}
	if h.ctx == nil {
		h.ctx = v.ctx
		if h.ctx == nil {
			h.ctx = context.Background()
		}
	}
	if h.log == nil {
		h.log = logging.MustGetLogger("skymail")
	}
	if v.dmsgC == nil {
		return fail(errors.New("no dmsg client"))
	}
	if v.forwardedPorts != nil && v.forwardedPorts.Get(skymailPort) != nil {
		return fail(errHostMTA)
	}

	v.initLock.Lock()
	dir := skymailDir(v.conf)
	_, limits, _ := skymailEffective(v.conf) //nolint:errcheck // reported at init
	v.initLock.Unlock()
	rt := &skymailRuntime{dir: dir}
	saved, _ := loadSkymailSaved(dir) //nolint:errcheck // reported at init
	rt.relay.Store(&saved.skymailRelay)
	mb, err := skymail.Open(skymail.Config{
		Dir: dir,
		PK:  v.conf.PK,
		PeerPK: func(c net.Conn) (cipher.PubKey, bool) {
			return remotePK(c.RemoteAddr())
		},
		Dialers: map[string]skymailbridge.Dialer{
			".skynet": skynetMailDialer{},
			".dmsg":   &visorDmsgDialer{c: v.dmsgC},
		},
		Limits: limits,
		Outbox: skymailOffline,
		SK:     skymailSK(v.conf.SK),
		Helpers: func() []cipher.PubKey {
			h, _ := v.mailHelpers(rt)
			return h
		},
		RelayFor: func(pk cipher.PubKey) bool {
			r, _ := v.mailRelayFor(rt)
			return slices.Contains(r, pk)
		},
		Park:      v.parkMail(),
		OnDeliver: v.notifyMail,
		Log:       h.log,
	})
	if err != nil {
		return fail(err)
	}
	lis, err := v.dmsgC.Listen(skymailPort)
	if err != nil {
		return fail(fmt.Errorf("dmsg port 25: %w", err))
	}

	ctx, cancel := context.WithCancel(h.ctx)
	rt.mb, rt.cancel = mb, cancel
	rt.track(lis)
	log := h.log
	go func() {
		if err := mb.Serve(ctx, lis); err != nil {
			log.WithError(err).Warn("mailbox: dmsg listener stopped")
		}
	}()
	goServeSkynetMirror(ctx, v.conf.PK, skymailPort, "skymail", log, func(l net.Listener) {
		rt.track(l)
		if err := mb.Serve(ctx, l); err != nil {
			log.WithError(err).Warn("mailbox: skynet listener stopped")
		}
	})
	go mb.RunExpiry(ctx)
	go mb.RunOutbox(ctx)
	if skymailOffline {
		// Mail parked on dmsg-async servers while this visor was offline.
		if err := v.dmsgC.HandleAsync(skymailPort, func(m dmsg.AsyncMessage) error {
			return mb.DeliverSealed(m.Payload, m.Server)
		}); err != nil {
			log.WithError(err).Debug("mailbox: not fetching parked mail")
		}
	}

	h.rt, h.reason = rt, ""
	lim := mb.Limits()
	log.WithField("address", mb.Address("", ".skynet")).WithField("dir", dir).
		WithField("max_total_size", lim.MaxTotalSize).WithField("max_age", lim.MaxAge).
		Info("Mailbox running")
	return nil
}

// stopSkymail takes the mailbox down, keeping its mail.
func (v *Visor) stopSkymail(reason string) {
	h := &v.mail
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rt != nil {
		h.rt.stop()
		h.rt = nil
		if skymailOffline && v.dmsgC != nil {
			_ = v.dmsgC.HandleAsync(skymailPort, nil) //nolint:errcheck // only fails when fetching is off
		}
	}
	h.reason = reason
}

// parkMail leaves sealed mail on port 25 with dmsg-async servers, for the
// desktop and wasm visors. It says too big only when every server it chose
// refused the size, so a message one server could still take is retried.
func (v *Visor) parkMail() func(context.Context, cipher.PubKey, []byte) ([]cipher.PubKey, error) {
	if !skymailOffline || v.dmsgC == nil {
		return nil
	}
	return func(ctx context.Context, dst cipher.PubKey, env []byte) ([]cipher.PubKey, error) {
		servers, err := v.dmsgC.Deposit(ctx, dst, skymailPort, env)
		switch {
		case err == nil:
			return servers, nil
		case errors.Is(err, dmsg.ErrAsyncNoServers):
			return nil, skymail.ErrNowhereToPark
		case allErrorsAre(err, dmsg.ErrAsyncTooBig):
			return nil, skymail.ErrTooBigToPark
		}
		return nil, err
	}
}

// allErrorsAre reports whether err, and each error it joins, is target.
func allErrorsAre(err, target error) bool {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		errs := j.Unwrap()
		for _, e := range errs {
			if !allErrorsAre(e, target) {
				return false
			}
		}
		return len(errs) > 0
	}
	return errors.Is(err, target)
}

// notifyMail announces new mail, under the mailbox's own app name so a
// phone files it in a channel of its own.
func (v *Visor) notifyMail(d skymail.Delivered) {
	hub := v.NotifyHub()
	if hub == nil {
		return
	}
	subject := d.Subject
	if subject == "" {
		subject = "(no subject)"
	}
	hub.Publish(appserver.NotifyReq{App: skymailApp, Title: mailSender(d.From), Body: subject, Tag: d.ID})
}

// mailSender is a sender short enough for a notification title: the display
// name when there is one, else the address with its 53-character key cut.
func mailSender(from string) string {
	a, err := mail.ParseAddress(from)
	if err != nil {
		return from
	}
	if a.Name != "" {
		return a.Name
	}
	local, domain, ok := strings.Cut(a.Address, "@")
	if !ok || len(domain) <= 16 {
		return a.Address
	}
	return local + "@" + domain[:8] + "…" + domain[strings.LastIndex(domain, "."):]
}

// skymailSK is the key the mailbox opens sealed envelopes with, where
// that is on.
func skymailSK(sk cipher.SecKey) cipher.SecKey {
	if !skymailOffline {
		return cipher.SecKey{}
	}
	return sk
}

// skynetMailDialer reaches a .skynet address over a skywire route and
// nothing else: the suffix is the sender's choice of network, so a
// failed route is reported, not quietly replaced with dmsg. (A .dmsg
// address goes to the dmsg client, whose sessions may themselves ride
// skynet; that choice is dmsg's.)
type skynetMailDialer struct{}

func (skynetMailDialer) Dial(ctx context.Context, peer cipher.PubKey, port uint16) (net.Conn, error) {
	return appnet.DialContext(ctx, appnet.Addr{Net: appnet.TypeSkynet, PubKey: peer, Port: routing.Port(port)})
}

func (v *Visor) mailbox() (*skymailRuntime, error) {
	v.mail.mu.Lock()
	defer v.mail.mu.Unlock()
	if v.mail.rt == nil {
		return nil, errMailboxOff
	}
	return v.mail.rt, nil
}

// MailStatus implements visorapi.API.
func (v *Visor) MailStatus() (*visorapi.MailStatus, error) {
	v.initLock.Lock()
	enabled, limits, _ := skymailEffective(v.conf) //nolint:errcheck // a status line
	v.initLock.Unlock()
	limits = limits.WithDefaults()
	st := &visorapi.MailStatus{Enabled: enabled, Limits: limits, Whitelist: []cipher.PubKey{}}
	rt, err := v.mailbox()
	if err != nil {
		v.mail.mu.Lock()
		st.Reason = v.mail.reason
		v.mail.mu.Unlock()
		if st.Reason == "" {
			st.Reason = "not started"
		}
		return st, nil
	}
	st.Running = true
	st.Address = rt.mb.Address("", ".skynet")
	st.AddressDmsg = rt.mb.Address("", ".dmsg")
	st.Dir = rt.dir
	st.Whitelist = rt.mb.Whitelist()
	st.Limits = rt.mb.Limits()
	st.Usage, _ = rt.mb.Usage() //nolint:errcheck // a status line, not a decision
	st.HasOutbox, st.Outbox = rt.mb.HasOutbox(), rt.mb.Queued()
	if st.HasOutbox {
		st.Helpers, st.HelpersDefault = v.mailHelpers(rt)
		st.RelayFor, st.RelayForDefault = v.mailRelayFor(rt)
	}
	if inbox, err := rt.mb.List(skymail.FolderInbox); err == nil {
		st.Total = len(inbox)
		for _, m := range inbox {
			if !m.Seen {
				st.Unread++
			}
		}
	}
	return st, nil
}

// MailSetSettings implements visorapi.API: it applies at once and is
// kept in the mailbox's settings.json, which survives a restart and, in
// a browser tab, a reload.
func (v *Visor) MailSetSettings(u visorapi.MailSettingsUpdate) error {
	v.initLock.Lock()
	dir := skymailDir(v.conf)
	v.initLock.Unlock()
	s, err := loadSkymailSaved(dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", skymailSettingsFile, err)
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
		d := visorconfig.Duration(*u.MaxAge)
		s.MaxAge = &d
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
	if err := s.save(dir); err != nil {
		return fmt.Errorf("save %s: %w", skymailSettingsFile, err)
	}

	v.initLock.Lock()
	enable, limits, _ := skymailEffective(v.conf) //nolint:errcheck // just written
	v.initLock.Unlock()
	if !enable {
		if v.appL == nil || v.StopApp(skymailApp) != nil {
			v.stopSkymail("disabled")
		}
		v.mail.mu.Lock()
		v.mail.reason = "disabled"
		v.mail.mu.Unlock()
		return nil
	}
	if _, err := v.mailbox(); err != nil {
		start := v.startSkymail // no launcher: run it directly
		if v.appL != nil {
			start = func() error { return v.StartApp(skymailApp) }
		}
		if err := start(); err != nil {
			return err
		}
	}
	if rt, err := v.mailbox(); err == nil {
		rt.mb.SetLimits(limits)
		rt.relay.Store(&s.skymailRelay)
		_ = rt.mb.Retry("") //nolint:errcheck // a new helper takes queued mail now
	}
	return nil
}

// MailList implements visorapi.API.
func (v *Visor) MailList(folder string) ([]skymail.Summary, error) {
	rt, err := v.mailbox()
	if err != nil {
		return nil, err
	}
	return rt.mb.List(folder)
}

// MailRead implements visorapi.API.
func (v *Visor) MailRead(folder, id string) (*skymail.Rendered, error) {
	rt, err := v.mailbox()
	if err != nil {
		return nil, err
	}
	raw, err := rt.mb.Raw(folder, id)
	if err != nil {
		return nil, err
	}
	r, err := skymail.Render(raw)
	if err != nil {
		return nil, err
	}
	_ = rt.mb.MarkSeen(folder, id) //nolint:errcheck // shown is what matters; the flag is cosmetic
	return r, nil
}

// MailRaw implements visorapi.API.
func (v *Visor) MailRaw(folder, id string) ([]byte, error) {
	rt, err := v.mailbox()
	if err != nil {
		return nil, err
	}
	return rt.mb.Raw(folder, id)
}

// MailAttachment implements visorapi.API.
func (v *Visor) MailAttachment(folder, id string, n int) (*skymail.AttachmentData, error) {
	rt, err := v.mailbox()
	if err != nil {
		return nil, err
	}
	return rt.mb.Attachment(folder, id, n)
}

// MailSend implements visorapi.API.
func (v *Visor) MailSend(msg skymail.Outgoing) (*skymail.SendResult, error) {
	rt, err := v.mailbox()
	if err != nil {
		return nil, err
	}
	ctx := v.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return rt.mb.Send(ctx, msg)
}

// MailDelete implements visorapi.API.
func (v *Visor) MailDelete(folder, id string) error {
	rt, err := v.mailbox()
	if err != nil {
		return err
	}
	return rt.mb.Delete(folder, id)
}

// MailRetry implements visorapi.API.
func (v *Visor) MailRetry(id string) error {
	rt, err := v.mailbox()
	if err != nil {
		return err
	}
	return rt.mb.Retry(id)
}

// MailSetWhitelist implements visorapi.API.
func (v *Visor) MailSetWhitelist(pks []cipher.PubKey) error {
	rt, err := v.mailbox()
	if err != nil {
		return err
	}
	return rt.mb.SetWhitelist(pks)
}
