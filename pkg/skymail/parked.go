// Package skymail pkg/skymail/parked.go c4-app-mail
package skymail

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skymailbridge"
)

// Parking leaves a sealed copy of queued mail with the dmsg servers that
// hold messages for offline visors (dmsg-async, docs/design/dmsg-async.md).
// The recipient's visor fetches it when it comes online, even if the sender
// and all its helpers are gone by then. It takes only small mail: a server
// advertises the largest message it holds, 64 KiB by default, and no
// server holds mail unless its operator turned dmsg-async on.
//
// The sender keeps its own copy and keeps trying, and the recipient files a
// repeat once, as with helpers.

// Errors a Park function returns that change what the Outbox does next.
var (
	// ErrTooBigToPark: no server takes a message this large, so the Outbox
	// stops trying to park it.
	ErrTooBigToPark = errors.New("skymail: too big for any dmsg server to hold")
	// ErrNowhereToPark: no dmsg server holds messages now. The Outbox
	// tries again later.
	ErrNowhereToPark = errors.New("skymail: no dmsg server holds messages")
)

// plan is what the Outbox worker needs to judge when an item is due.
func (mb *Mailbox) plan() outboxPlan {
	return outboxPlan{
		maxAge:  mb.Limits().MaxAge,
		helpers: mb.helpers(),
		park:    mb.cfg.Park != nil && mb.sealing(),
	}
}

// park leaves st's message with dmsg servers for every pending recipient
// visor that has no parked copy yet.
func (mb *Mailbox) park(ctx context.Context, st *queueItem, msg []byte) {
	if st.rec.Parked == nil {
		st.rec.Parked = map[string][]string{}
	}
	groups, _ := groupRcpts(st.rec.Pending)
	missing := 0
	for _, g := range groups {
		key := g.pk.Hex()
		if len(st.rec.Parked[key]) > 0 || g.pk == mb.cfg.PK {
			continue
		}
		env, err := Seal(msg, mb.cfg.SK, g.pk)
		if err != nil {
			mb.log.WithError(err).Warn("skymail: seal for parking")
			missing++
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, attemptTimeout)
		servers, err := mb.cfg.Park(pctx, g.pk, env)
		cancel()
		switch {
		case errors.Is(err, ErrTooBigToPark):
			// Size does not change with time, so stop asking.
			st.rec.NoPark = true
			mb.log.WithField("bytes", len(env)).Debug("skymail: too big to park")
			return
		case err != nil:
			missing++
			mb.log.WithError(err).WithField("rcpt_pk", key).Debug("skymail: not parked")
			continue
		}
		for _, s := range servers {
			st.rec.Parked[key] = append(st.rec.Parked[key], s.Hex())
		}
		mb.log.WithField("rcpt_pk", key).WithField("servers", len(servers)).Info("skymail: parked with dmsg servers")
	}
	if missing == 0 {
		st.parkTries, st.parkNext = 0, time.Time{}
		return
	}
	st.parkTries++
	st.parkNext = time.Now().Add(retryDelay(st.parkTries))
}

// parkedServers counts the distinct servers that hold a copy.
func parkedServers(parked map[string][]string) int {
	var all []string
	for _, servers := range parked {
		for _, s := range servers {
			if !slices.Contains(all, s) {
				all = append(all, s)
			}
		}
	}
	return len(all)
}

// DeliverSealed files a sealed envelope fetched from dmsg server `via`.
// It answers nil when the envelope is filed, was filed before, or can
// never be (not sealed for this visor, a broken seal, a sender this
// mailbox refuses), so the server deletes it. It answers an error when
// it may work later, such as a full mailbox, so the server keeps it.
func (mb *Mailbox) DeliverSealed(env []byte, via cipher.PubKey) error {
	if !mb.sealing() {
		return errors.New("skymail: this mailbox does not open sealed mail")
	}
	msg, signer, err := OpenSealed(env, mb.cfg.SK)
	if err != nil {
		mb.log.WithError(err).WithField("via", via.Hex()).Warn("skymail: dropped a parked message that does not open")
		return nil
	}
	if signer == via {
		via = cipher.PubKey{}
	}
	_, err = mb.file(signer, via, msg)
	var r *skymailbridge.Reply
	if errors.As(err, &r) && r.Code >= 500 {
		mb.log.WithField("from_pk", signer.Hex()).WithField("reason", r.Text).Info("skymail: dropped a parked message")
		return nil
	}
	return err
}
