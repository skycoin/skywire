// Package skymail pkg/skymail/parked.go c3-app-mail
//
// Parking: mail for a visor the Outbox cannot reach is also left, sealed for
// that visor, on its dmsg-async inbox hosts, so it arrives when that visor
// comes back even if this one is offline by then. The visor fetches parked
// mail and files it through the same checks as mail over SMTP.
package skymail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skymailbridge"
)

// Parker reaches the visor's dmsg-async inbox (pkg/dmsg/dmsgasync).
type Parker interface {
	// Park leaves env, sealed for rcpt, on rcpt's inbox hosts and returns the
	// host that took it. ref names the message across retries.
	Park(ctx context.Context, rcpt cipher.PubKey, env []byte, ref string) (cipher.PubKey, error)
	// Unpark returns the mail parked for this visor without removing it.
	Unpark(ctx context.Context) ([]Parked, error)
	// Ack removes parked mail.
	Ack(ctx context.Context, done []Parked) error
}

// Parked is a sealed envelope that waited for this visor on an inbox host.
type Parked struct {
	ID   string
	Host cipher.PubKey
	From cipher.PubKey
	Body []byte
}

// unparkInterval is how often parked mail is fetched while online.
const unparkInterval = 2 * time.Minute

// parkRef names one Outbox message for one recipient visor, so a retried
// park is stored once.
func parkRef(id string, rcpt cipher.PubKey) string {
	h := sha256.Sum256(append([]byte(id+"/"), rcpt[:]...))
	return hex.EncodeToString(h[:16])
}

// park leaves the message, sealed per pending recipient visor, on each
// visor's inbox hosts, once per visor.
func (mb *Mailbox) park(ctx context.Context, id string, st *queueItem, msg []byte) {
	groups, _ := groupRcpts(st.rec.Pending)
	missing := 0
	for _, g := range groups {
		if slices.Contains(st.rec.Parked, g.pk.Hex()) {
			continue
		}
		env, err := mb.cfg.Sealer.Seal(msg, g.pk)
		if err != nil {
			mb.log.WithError(err).Debug("skymail: seal for parking")
			missing++
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, attemptTimeout)
		host, err := mb.cfg.Parker.Park(pctx, g.pk, env, parkRef(id, g.pk))
		cancel()
		if err != nil {
			mb.log.WithError(err).WithField("rcpt", g.pk.Hex()).Debug("skymail: not parked")
			missing++
			continue
		}
		st.rec.Parked = append(st.rec.Parked, g.pk.Hex())
		mb.log.WithField("rcpt", g.pk.Hex()).WithField("host", host.Hex()).Info("skymail: parked for an offline visor")
	}
	if missing == 0 {
		st.parkTries, st.parkNext = 0, time.Time{}
		return
	}
	st.parkTries++
	st.parkNext = time.Now().Add(retryDelay(st.parkTries))
}

// parkIfDue parks when parking falls due within `within`.
func (mb *Mailbox) parkIfDue(ctx context.Context, id string, st *queueItem, msg []byte, within time.Duration) {
	if mb.out.parking && st.needsPark() && !st.parkNext.After(time.Now().Add(within)) {
		mb.park(ctx, id, st, msg)
	}
}

// RunParked fetches the mail parked for this visor when it starts and every
// unparkInterval until ctx ends.
func (mb *Mailbox) RunParked(ctx context.Context) {
	if mb.cfg.Parker == nil || !mb.sealing() {
		return
	}
	t := time.NewTicker(unparkInterval)
	defer t.Stop()
	for {
		mb.Unpark(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Unpark files the mail parked for this visor and removes what it filed or
// can never file. Mail that may file later, such as into a full mailbox,
// stays parked. It reports how many messages it filed.
func (mb *Mailbox) Unpark(ctx context.Context) int {
	if mb.cfg.Parker == nil || !mb.sealing() {
		return 0
	}
	list, err := mb.cfg.Parker.Unpark(ctx)
	if err != nil {
		mb.log.WithError(err).Debug("skymail: fetch parked mail")
		return 0
	}
	var done []Parked
	filed := 0
	for _, p := range list {
		_, err := mb.file(p.Host, p.Body, true)
		var rep *skymailbridge.Reply
		switch {
		case err == nil:
			filed++
			done = append(done, p)
		case errors.As(err, &rep) && rep.Code >= 500:
			mb.log.WithField("host", p.Host.Hex()).WithField("reason", rep.Text).Info("skymail: dropped parked mail")
			done = append(done, p)
		default:
			mb.log.WithError(err).Debug("skymail: parked mail kept for later")
		}
	}
	if len(done) > 0 {
		if err := mb.cfg.Parker.Ack(ctx, done); err != nil {
			mb.log.WithError(err).Debug("skymail: remove parked mail")
		}
	}
	return filed
}
