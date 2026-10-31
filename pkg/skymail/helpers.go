// Package skymail pkg/skymail/helpers.go c4-app-mail
package skymail

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skymailbridge"
)

// Helpers are other visors of the same user that carry its queued mail,
// so it still arrives after this visor goes away (a closed browser tab,
// a laptop asleep). Each helper gets the message sealed for its
// recipient, keeps it in its own Outbox and delivers it like its own.
// The helper cannot read it and the recipient files it under its
// signer. This visor keeps its own copy and keeps trying too, and the
// recipient files a repeat only once (seen.go).
//
// A helper takes such mail only from the visors Config.RelayFor names.

// helpers is who to hand queued mail to now.
func (mb *Mailbox) helpers() []cipher.PubKey {
	if mb.cfg.Helpers == nil || !mb.sealing() {
		return nil
	}
	var out []cipher.PubKey
	for _, h := range mb.cfg.Helpers() {
		if h != mb.cfg.PK && !h.Null() && !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	return out
}

// relays reports whether this mailbox carries sealed mail for peer.
func (mb *Mailbox) relays(peer cipher.PubKey) bool {
	return mb.out != nil && mb.sealing() && mb.cfg.RelayFor != nil && peer != mb.cfg.PK && mb.cfg.RelayFor(peer)
}

// handOff offers st's message to every helper that lacks it, once.
func (mb *Mailbox) handOff(ctx context.Context, st *queueItem, msg []byte, helpers []cipher.PubKey) {
	missing := 0
	for _, h := range helpers {
		if slices.Contains(st.rec.Handed, h.Hex()) {
			continue
		}
		hctx, cancel := context.WithTimeout(ctx, attemptTimeout)
		err := mb.handTo(hctx, h, st.rec, msg)
		cancel()
		if err != nil {
			missing++
			mb.log.WithError(err).WithField("helper", h.Hex()).Debug("skymail: helper did not take the message")
			continue
		}
		st.rec.Handed = append(st.rec.Handed, h.Hex())
		mb.log.WithField("helper", h.Hex()).WithField("pending", len(st.rec.Pending)).Info("skymail: handed to helper")
	}
	if missing == 0 {
		st.handTries, st.handNext = 0, time.Time{}
		return
	}
	st.handTries++
	st.handNext = time.Now().Add(retryDelay(st.handTries))
}

// handTo gives helper one envelope per pending recipient visor.
func (mb *Mailbox) handTo(ctx context.Context, helper cipher.PubKey, rec queueRecord, msg []byte) error {
	groups, _ := groupRcpts(rec.Pending)
	for _, g := range groups {
		if g.pk == helper {
			// A helper that is itself the recipient is not away.
			continue
		}
		env, err := Seal(msg, mb.cfg.SK, g.pk)
		if err != nil {
			return err
		}
		d := mb.helperDialer(helper)
		if d == nil {
			return fmt.Errorf("no network to reach helper %s", helper)
		}
		cfg := skymailbridge.Config{
			Suffix: g.suffix, Mode: "b", HeloName: mb.cfg.PK.DNSLabel() + Suffixes[1], RemotePort: 25,
		}
		if err := skymailbridge.Relay(ctx, &recordingDialer{d: d, life: ctx}, cfg, rec.From, g.rcpts, env, mb.log); err != nil {
			return err
		}
	}
	return nil
}

// helperDialer reaches a helper over dmsg, where every visor is, or over
// skynet when this mailbox has no dmsg. It dials the helper whatever
// recipient Relay asks for.
func (mb *Mailbox) helperDialer(helper cipher.PubKey) skymailbridge.Dialer {
	for _, sfx := range []string{".dmsg", ".skynet"} {
		if d := mb.cfg.Dialers[sfx]; d != nil {
			return fixedPeer{d: d, pk: helper}
		}
	}
	return nil
}

type fixedPeer struct {
	d  skymailbridge.Dialer
	pk cipher.PubKey
}

func (f fixedPeer) Dial(ctx context.Context, _ cipher.PubKey, port uint16) (net.Conn, error) {
	return f.d.Dial(ctx, f.pk, port)
}

// carry queues a sealed envelope a designated visor handed over, to be
// delivered at once and then like any Outbox message.
func (mb *Mailbox) carry(peer cipher.PubKey, env skymailbridge.Envelope) (string, error) {
	rcptPK, ok := SealedRecipient(env.Body)
	if !ok {
		return "", &skymailbridge.Reply{Code: 554, Enhanced: "5.6.0", Text: "only sealed mail is carried"}
	}
	for _, r := range env.Rcpts {
		if pk, ok := addressPK(r); !ok || pk != rcptPK {
			return "", &skymailbridge.Reply{Code: 554, Enhanced: "5.7.1", Text: "envelope is sealed for another visor than " + r}
		}
	}
	id, err := mb.enqueue(env.Body, queueRecord{From: env.From, Pending: env.Rcpts, Carried: peer.Hex()}, time.Time{})
	if err == ErrMailboxFull {
		return "", &skymailbridge.Reply{Code: 452, Enhanced: "4.2.2", Text: "mailbox full"}
	}
	if err != nil {
		mb.log.WithError(err).Warn("skymail: queue carried mail")
		return "", &skymailbridge.Reply{Code: 451, Enhanced: "4.3.0", Text: "outbox store failed"}
	}
	mb.log.WithField("for", peer.Hex()).WithField("to", strings.Join(env.Rcpts, ",")).WithField("id", id).
		Info("skymail: carrying sealed mail")
	return "Ok: carrying " + id, nil
}

// SealedRecipient reads the recipient PK an envelope is sealed for,
// which a carrier can see without opening it.
func SealedRecipient(body []byte) (cipher.PubKey, bool) {
	var none cipher.PubKey
	if !IsSealed(body) {
		return none, false
	}
	_, b64, ok := bytes.Cut(body, []byte("\r\n\r\n"))
	if !ok {
		return none, false
	}
	var head []byte
	for _, line := range bytes.Split(b64, []byte("\r\n")) {
		head = append(head, bytes.TrimSpace(line)...)
		if len(head) >= 96 {
			break
		}
	}
	if len(head) < 96 {
		return none, false
	}
	raw, err := base64.StdEncoding.DecodeString(string(head[:96]))
	if err != nil || string(raw[:len(sealedMagic)]) != sealedMagic {
		return none, false
	}
	var pk cipher.PubKey
	copy(pk[:], raw[len(sealedMagic)+pkLen:])
	return pk, true
}
