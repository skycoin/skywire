// Package visor pkg/visor/skymail.go c3-vis-core
//
// The visor's own mailbox (pkg/skymail). It listens for SMTP on port 25
// over dmsg and skynet, like every dual-listened visor port, and sends
// by dialing the recipient visor's port 25 directly: no MTA, no queue,
// no certificate, so it runs as-is in a browser tab. It is on by
// default everywhere; the size and age limits keep an open mailbox from
// filling the storage it lives in. Enable and every limit are live
// settings (MailSetSettings), persisted to the config.
package visor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/skymailbridge"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// skymailPort is SMTP's port, on dmsg and skynet alike.
const skymailPort = 25

// skymailSkynetDialTimeout bounds the skynet attempt of a .skynet
// delivery before it falls back to dmsg, so a peer without a route yet
// is still reached within the relay's own dial budget.
const skymailSkynetDialTimeout = 12 * time.Second

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

func initSkymail(ctx context.Context, v *Visor, log *logging.Logger) error {
	v.mail.mu.Lock()
	v.mail.ctx, v.mail.log = ctx, log
	v.mail.mu.Unlock()
	if v.conf == nil || !skymailEnabled(v.conf) {
		v.mail.mu.Lock()
		v.mail.reason = "disabled"
		v.mail.mu.Unlock()
		log.Debug("mailbox off")
		return nil
	}
	if v.dmsgC != nil {
		select {
		case <-v.dmsgC.Ready():
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := v.startSkymail(); err != nil {
		// A mailbox that cannot start must not take the visor with it.
		log.WithError(err).Warn("mailbox not started")
	}
	return nil
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
		return fail(errors.New("port 25 is forwarded (`serve add 25`) to the host's MTA"))
	}

	v.initLock.Lock()
	dir, limits := skymailDir(v.conf), skymailLimits(v.conf)
	v.initLock.Unlock()
	mb, err := skymail.Open(skymail.Config{
		Dir: dir,
		PK:  v.conf.PK,
		PeerPK: func(c net.Conn) (cipher.PubKey, bool) {
			return remotePK(c.RemoteAddr())
		},
		Dialers: map[string]skymailbridge.Dialer{
			".skynet": skynetThenDmsgDialer{dmsgC: v.dmsgC},
			".dmsg":   &visorDmsgDialer{c: v.dmsgC},
		},
		Limits: limits,
		Log:    h.log,
	})
	if err != nil {
		return fail(err)
	}
	lis, err := v.dmsgC.Listen(skymailPort)
	if err != nil {
		return fail(fmt.Errorf("dmsg port 25: %w", err))
	}

	ctx, cancel := context.WithCancel(h.ctx)
	rt := &skymailRuntime{mb: mb, dir: dir, cancel: cancel}
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
	}
	h.reason = reason
}

// skynetThenDmsgDialer reaches a .skynet address over a route when one
// can be built in time, and over dmsg otherwise. Both authenticate the
// peer by PK, which is all the mailbox relies on.
type skynetThenDmsgDialer struct{ dmsgC *dmsg.Client }

func (d skynetThenDmsgDialer) Dial(ctx context.Context, peer cipher.PubKey, port uint16) (net.Conn, error) {
	sctx, cancel := context.WithTimeout(ctx, skymailSkynetDialTimeout)
	conn, err := appnet.DialContext(sctx, appnet.Addr{Net: appnet.TypeSkynet, PubKey: peer, Port: routing.Port(port)})
	cancel()
	if err == nil {
		return conn, nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("skynet: %w", err)
	}
	conn, derr := d.dmsgC.Dial(ctx, dmsg.Addr{PK: peer, Port: port})
	if derr != nil {
		return nil, fmt.Errorf("skynet: %v; dmsg: %w", err, derr)
	}
	return conn, nil
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
	enabled, limits := skymailEnabled(v.conf), skymailLimits(v.conf).WithDefaults()
	v.initLock.Unlock()
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

// MailSetSettings implements visorapi.API: it applies at once and
// persists to the config.
func (v *Visor) MailSetSettings(u visorapi.MailSettingsUpdate) error {
	v.initLock.Lock()
	if v.conf.Skymail == nil {
		v.conf.Skymail = &visorconfig.SkymailConfig{Enable: skymailEnabled(v.conf)}
	}
	s := v.conf.Skymail
	if u.Enable != nil {
		s.Enable = *u.Enable
	}
	if u.MaxMessageSize != nil {
		s.MaxMessageSize = *u.MaxMessageSize
	}
	if u.MaxTotalSize != nil {
		s.MaxTotalSize = *u.MaxTotalSize
	}
	if u.MaxAge != nil {
		s.MaxAge = visorconfig.Duration(*u.MaxAge)
	}
	enable, limits := s.Enable, skymailLimits(v.conf)
	v.initLock.Unlock()

	var err error
	if enable {
		err = v.startSkymail()
		if rt, rerr := v.mailbox(); rerr == nil {
			rt.mb.SetLimits(limits)
		}
	} else {
		v.stopSkymail("disabled")
	}
	if ferr := v.conf.Flush(); ferr != nil && err == nil {
		err = fmt.Errorf("applied, but not saved: %w", ferr)
	}
	return err
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

// MailSetWhitelist implements visorapi.API.
func (v *Visor) MailSetWhitelist(pks []cipher.PubKey) error {
	rt, err := v.mailbox()
	if err != nil {
		return err
	}
	return rt.mb.SetWhitelist(pks)
}
