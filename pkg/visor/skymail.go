// Package visor pkg/visor/skymail.go c3-vis-core
//
// The visor's own mailbox (pkg/skymail). It listens for SMTP on port 25
// over dmsg and skynet, like every dual-listened visor port, and sends
// by dialing the recipient visor's port 25 directly: no MTA, no queue,
// no certificate, so it runs as-is in a browser tab.
package visor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"runtime"
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

type skymailRuntime struct {
	mb  *skymail.Mailbox
	dir string
}

// skymailEnabled: an explicit section decides; without one the mailbox
// runs on js/wasm, where no MTA can, and stays off on native hosts,
// which may already forward port 25 to Postfix.
func skymailEnabled(conf *visorconfig.V1) bool {
	if conf != nil && conf.Skymail != nil {
		return conf.Skymail.Enable
	}
	return runtime.GOOS == "js"
}

// skymailDir defaults to "mail" beside the local path, not inside it:
// the wasm visor never persists local/, and mail must survive a reload.
func skymailDir(conf *visorconfig.V1) string {
	if conf.Skymail != nil && conf.Skymail.Dir != "" {
		return conf.Skymail.Dir
	}
	return filepath.Join(filepath.Dir(filepath.Clean(conf.LocalPath)), "mail")
}

func initSkymail(ctx context.Context, v *Visor, log *logging.Logger) error {
	if v.conf == nil || !skymailEnabled(v.conf) {
		log.Debug("mailbox off")
		return nil
	}
	if v.dmsgC == nil {
		log.Warn("mailbox enabled but no dmsg client; not starting")
		return nil
	}
	if v.forwardedPorts != nil && v.forwardedPorts.Get(skymailPort) != nil {
		log.Info("port 25 is forwarded (`serve add 25`), so it belongs to the host's MTA; mailbox not started")
		return nil
	}
	select {
	case <-v.dmsgC.Ready():
	case <-ctx.Done():
		return ctx.Err()
	}

	dir := skymailDir(v.conf)
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
		Log: log,
	})
	if err != nil {
		// A broken mailbox must not take the visor down with it.
		log.WithError(err).Warn("mailbox not started")
		return nil
	}

	lis, err := v.dmsgC.Listen(skymailPort)
	if err != nil {
		log.WithError(err).Warn("mailbox: dmsg port 25 unavailable; not started")
		return nil
	}
	go func() {
		if err := mb.Serve(ctx, lis); err != nil {
			log.WithError(err).Warn("mailbox: dmsg listener stopped")
		}
	}()
	goServeSkynetMirror(ctx, v.conf.PK, skymailPort, "skymail", log, func(l net.Listener) {
		if err := mb.Serve(ctx, l); err != nil {
			log.WithError(err).Warn("mailbox: skynet listener stopped")
		}
	})

	v.initLock.Lock()
	v.skymail = &skymailRuntime{mb: mb, dir: dir}
	v.initLock.Unlock()
	log.WithField("address", mb.Address("", ".skynet")).WithField("dir", dir).Info("Mailbox running")
	return nil
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
	v.initLock.Lock()
	defer v.initLock.Unlock()
	if v.skymail == nil {
		return nil, errMailboxOff
	}
	return v.skymail, nil
}

// MailStatus implements visorapi.API.
func (v *Visor) MailStatus() (*visorapi.MailStatus, error) {
	rt, err := v.mailbox()
	if err != nil {
		reason := "disabled"
		if v.conf != nil && skymailEnabled(v.conf) {
			reason = "enabled but not started; see the visor log"
		}
		return &visorapi.MailStatus{Reason: reason, Whitelist: []cipher.PubKey{}}, nil
	}
	st := &visorapi.MailStatus{
		Running:     true,
		Address:     rt.mb.Address("", ".skynet"),
		AddressDmsg: rt.mb.Address("", ".dmsg"),
		Dir:         rt.dir,
		Whitelist:   rt.mb.Whitelist(),
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

// MailSend implements visorapi.API.
func (v *Visor) MailSend(msg skymail.Outgoing) (*skymail.SendResult, error) {
	rt, err := v.mailbox()
	if err != nil {
		return nil, err
	}
	return rt.mb.Send(v.ctx, msg)
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
