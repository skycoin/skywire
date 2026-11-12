// Package visor pkg/visor/skychat_async.go c3-vis-core
package visor

import (
	"time"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgasync"
	skychatgroup "github.com/skycoin/skywire/pkg/skychat/group"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// chatHost parks the skychat app's 1:1 messages on the dmsg-async inbox. The
// visor seals and signs them with its key, as it does mail.
type chatHost struct{ v *Visor }

func (v *Visor) appChatHost(appName string) appserver.ChatHost {
	if appName != skyenv.SkychatName || v.conf == nil {
		return nil
	}
	return chatHost{v}
}

func (h chatHost) ChatPark(rcpt cipher.PubKey, msg []byte, ref string) (cipher.PubKey, error) {
	env, err := skymail.Seal(msg, h.v.conf.SK, rcpt)
	if err != nil {
		return cipher.PubKey{}, err
	}
	res, err := h.v.DmsgAsyncSend(visorapi.DmsgAsyncSendRequest{To: rcpt, Port: appserver.ChatAsyncPort, Payload: env, Ref: ref})
	if err != nil {
		return cipher.PubKey{}, err
	}
	return res.Host, nil
}

func (h chatHost) ChatUnpark() ([]appserver.ParkedChat, error) {
	msgs, err := h.v.asyncPeek([]uint16{appserver.ChatAsyncPort})
	if err != nil {
		return nil, err
	}
	var out []appserver.ParkedChat
	var bad []appserver.ParkedRef
	for _, m := range msgs {
		msg, signer, err := skymail.OpenSealed(m.Payload, h.v.conf.SK)
		if err != nil {
			h.v.async.log.WithError(err).WithField("from", m.From.Hex()).Debug("dmsg-async: dropped a chat message that does not open")
			bad = append(bad, appserver.ParkedRef{Host: m.Host, ID: m.ID})
			continue
		}
		out = append(out, appserver.ParkedChat{ID: m.ID, Host: m.Host, From: signer, Msg: msg, At: m.Deposited})
	}
	if err := h.v.ackParked(bad); err != nil {
		h.v.async.log.WithError(err).Debug("dmsg-async: remove chat messages that do not open")
	}
	return out, nil
}

func (h chatHost) ChatAck(refs []appserver.ParkedRef) error { return h.v.ackParked(refs) }

// parkGroupJoins lets mgr leave join requests and decisions on dmsg-async
// inboxes, sealed and signed with the visor key, and takes the ones left here.
func (v *Visor) parkGroupJoins(mgr *skychatgroup.Manager) {
	mgr.SetParker(v.groupParker)
	v.AsyncHandle(skychatgroup.ParkPort, v.groupParkHandler(mgr.HandleParked))
}

func (v *Visor) groupParker(to cipher.PubKey, payload []byte) error {
	env, err := skymail.Seal(payload, v.conf.SK, to)
	if err != nil {
		return err
	}
	_, err = v.DmsgAsyncSend(visorapi.DmsgAsyncSendRequest{To: to, Port: skychatgroup.ParkPort, Payload: env})
	return err
}

// groupParkHandler opens a parked frame and hands it to take with its signer
// and the time the host took it.
// A frame that does not open is dropped.
func (v *Visor) groupParkHandler(take func(from cipher.PubKey, payload []byte, at time.Time) error) dmsgasync.Handler {
	return func(m dmsgasync.Message) error {
		msg, signer, err := skymail.OpenSealed(m.Payload, v.conf.SK)
		if err != nil {
			return nil
		}
		return take(signer, msg, m.Deposited)
	}
}
