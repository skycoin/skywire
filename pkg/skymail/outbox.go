// Package skymail pkg/skymail/outbox.go c4-app-mail
package skymail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/textproto"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// The Outbox keeps mail a recipient's visor could not take yet and tries
// again until it can, or until MaxAge gives up and a bounce says so. Only
// a temporary failure queues. A refusal (a 5xx reply) is final at once.

// queueDir holds one <id>.json record per Outbox message.
const queueDir = "queue"

const (
	// attemptTimeout bounds one delivery attempt, sized for a 10 MiB
	// message over a slow dmsg session.
	attemptTimeout = 10 * time.Minute
	// maxAttempts is how many queued messages go out at once.
	maxAttempts = 4
	// idleWait is how long the worker sleeps with nothing due.
	idleWait = time.Hour
)

// retryDelays is the wait after each attempt. The last one repeats.
var retryDelays = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}

func retryDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	return retryDelays[min(attempts, len(retryDelays))-1]
}

// Queued is where an Outbox message stands.
type Queued struct {
	// Pending are the recipients still to reach.
	Pending     []string  `json:"pending"`
	Attempts    int       `json:"attempts"`
	LastError   string    `json:"last_error,omitempty"`
	NextAttempt time.Time `json:"next_attempt"`
	// GiveUp is when the Outbox stops trying. Zero means never.
	GiveUp time.Time `json:"give_up,omitzero"`
	// Handed are the helpers that hold a sealed copy to deliver too.
	Handed []string `json:"handed,omitempty"`
	// CarriedFor is set on sealed mail this visor delivers for the
	// visor that signed it. This visor cannot read it.
	CarriedFor string `json:"carried_for,omitempty"`
	// Parked is how many dmsg servers hold a sealed copy for the
	// recipient to fetch when it comes online (parked.go).
	Parked int `json:"parked,omitempty"`
}

// queueRecord is what the Outbox keeps beside a message.
type queueRecord struct {
	From      string       `json:"from"`
	Pending   []string     `json:"pending"`
	Failed    []RcptResult `json:"failed,omitempty"`
	SentCopy  bool         `json:"sent_copy,omitempty"`
	Attempts  int          `json:"attempts"`
	LastError string       `json:"last_error,omitempty"`
	// Handed are the helpers (hex PKs) that took a sealed copy.
	Handed []string `json:"handed,omitempty"`
	// Carried is the hex PK this sealed envelope is carried for.
	Carried string `json:"carried,omitempty"`
	// Parked maps a recipient PK (hex) to the dmsg servers (hex) holding
	// a sealed copy for it. NoPark is set once no server would take one.
	Parked map[string][]string `json:"parked,omitempty"`
	NoPark bool                `json:"no_park,omitempty"`
}

type queueItem struct {
	rec  queueRecord
	next time.Time // delivery; zero is due now
	// handNext is when to offer the message to helpers that lack it.
	handNext  time.Time
	handTries int
	// parkNext is when to park it for recipients not parked yet.
	parkNext  time.Time
	parkTries int
	busy      bool
}

// outboxPlan is what the worker needs to know about the mailbox to judge
// when an item is next due.
type outboxPlan struct {
	maxAge  time.Duration
	helpers []cipher.PubKey
	park    bool
}

// needsPark reports whether a pending recipient has no parked copy yet.
func (it *queueItem) needsPark(on bool) bool {
	if !on || it.rec.Carried != "" || it.rec.NoPark {
		return false
	}
	for _, r := range it.rec.Pending {
		if g, _, err := parseRcpt(r); err == nil && len(it.rec.Parked[g.pk.Hex()]) == 0 {
			return true
		}
	}
	return false
}

// needsHandoff reports whether a helper in helpers lacks this message.
func (it *queueItem) needsHandoff(helpers []cipher.PubKey) bool {
	if it.rec.Carried != "" {
		return false
	}
	for _, h := range helpers {
		if !slices.Contains(it.rec.Handed, h.Hex()) {
			return true
		}
	}
	return false
}

// outbox is the Outbox folder and the delivery state of what is in it.
type outbox struct {
	md   *maildir
	dir  string
	kick chan struct{}

	mu    sync.Mutex
	items map[string]*queueItem
}

func openOutbox(root string) (*outbox, error) {
	md, err := openMaildir(filepath.Join(root, "."+FolderOutbox))
	if err != nil {
		return nil, err
	}
	o := &outbox{
		md:    md,
		dir:   filepath.Join(md.dir, queueDir),
		kick:  make(chan struct{}, 1),
		items: map[string]*queueItem{},
	}
	if err := os.MkdirAll(o.dir, 0o700); err != nil {
		return nil, fmt.Errorf("skymail: create outbox queue: %w", err)
	}
	des, err := os.ReadDir(o.dir)
	if err != nil {
		return nil, err
	}
	for _, de := range des {
		id, ok := strings.CutSuffix(de.Name(), ".json")
		if !ok || !validID(id) {
			continue
		}
		path := filepath.Join(o.dir, de.Name())
		if _, err := md.find(id); errors.Is(err, ErrNotFound) {
			// The message went before its record could.
			_ = os.Remove(path) //nolint:errcheck,gosec
			continue
		}
		raw, err := os.ReadFile(path) //nolint:gosec // our own file
		if err != nil {
			return nil, err
		}
		var rec queueRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil, fmt.Errorf("skymail: outbox record %s: %w", id, err)
		}
		// A restart is a fresh chance: the recipient may be back.
		o.items[id] = &queueItem{rec: rec}
	}
	return o, nil
}

func (o *outbox) recordPath(id string) string { return filepath.Join(o.dir, id+".json") }

func (o *outbox) writeRecord(id string, rec queueRecord) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	path := o.recordPath(id)
	if err := os.WriteFile(path+".tmp", raw, 0o600); err != nil {
		return fmt.Errorf("skymail: write outbox record: %w", err)
	}
	return os.Rename(path+".tmp", path)
}

func (o *outbox) poke() {
	select {
	case o.kick <- struct{}{}:
	default:
	}
}

// dueAt is when an item next needs the worker: a delivery attempt, a
// handoff to a helper that lacks it, parking it, or giving up.
func (it *queueItem) dueAt(id string, p outboxPlan) time.Time {
	at := it.next
	if it.needsHandoff(p.helpers) && it.handNext.Before(at) {
		at = it.handNext
	}
	if it.needsPark(p.park) && it.parkNext.Before(at) {
		at = it.parkNext
	}
	if g := giveUp(id, p.maxAge); !g.IsZero() && g.Before(at) {
		at = g
	}
	return at
}

// due marks the messages the worker should take now as busy and
// returns them.
func (o *outbox) due(now time.Time, p outboxPlan) []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	var ids []string
	for id, it := range o.items {
		if !it.busy && !it.dueAt(id, p).After(now) {
			it.busy = true
			ids = append(ids, id)
		}
	}
	return ids
}

// wait is how long until a message is next due.
func (o *outbox) wait(now time.Time, p outboxPlan) time.Duration {
	o.mu.Lock()
	defer o.mu.Unlock()
	d := idleWait
	for id, it := range o.items {
		if !it.busy {
			d = min(d, max(it.dueAt(id, p).Sub(now), 0))
		}
	}
	return d
}

// snapshot copies an item for an attempt to work on.
func (o *outbox) snapshot(id string) (queueItem, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	it, ok := o.items[id]
	if !ok {
		return queueItem{}, false
	}
	c := *it
	c.rec.Pending = slices.Clone(it.rec.Pending)
	c.rec.Failed = slices.Clone(it.rec.Failed)
	c.rec.Handed = slices.Clone(it.rec.Handed)
	c.rec.Parked = make(map[string][]string, len(it.rec.Parked))
	for k, v := range it.rec.Parked {
		c.rec.Parked[k] = slices.Clone(v)
	}
	return c, true
}

// release ends an attempt. With st set it keeps the new state, unless
// the message was deleted while the attempt ran.
func (o *outbox) release(id string, st *queueItem) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	it, ok := o.items[id]
	if !ok {
		return nil
	}
	it.busy = false
	if st == nil {
		return nil
	}
	it.rec, it.next, it.handNext, it.handTries = st.rec, st.next, st.handNext, st.handTries
	it.parkNext, it.parkTries = st.parkNext, st.parkTries
	return o.writeRecord(id, st.rec)
}

// drop removes a message and its record. The message goes first: a
// record left alone is cleared on the next open.
func (o *outbox) drop(id string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.items, id)
	err := o.md.remove(id)
	if rerr := os.Remove(o.recordPath(id)); rerr != nil && !errors.Is(rerr, os.ErrNotExist) && err == nil {
		err = rerr
	}
	return err
}

// wake makes due now every message with a pending recipient at pk, and
// when pk is a helper any handoff to it, or everything when all is set.
func (o *outbox) wake(pk cipher.PubKey, all, helper bool) {
	if o == nil {
		return
	}
	o.mu.Lock()
	n := 0
	for _, it := range o.items {
		if all || pendingAt(it.rec.Pending, pk) {
			it.next = time.Time{}
			n++
		}
		if all || helper && !slices.Contains(it.rec.Handed, pk.Hex()) {
			it.handNext = time.Time{}
			n++
		}
		if all {
			it.parkNext = time.Time{}
		}
	}
	o.mu.Unlock()
	if n > 0 {
		o.poke()
	}
}

func pendingAt(rcpts []string, pk cipher.PubKey) bool {
	for _, r := range rcpts {
		if g, _, err := parseRcpt(r); err == nil && g.pk == pk {
			return true
		}
	}
	return false
}

func (o *outbox) len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.items)
}

func (o *outbox) info(id string, maxAge time.Duration) *Queued {
	o.mu.Lock()
	defer o.mu.Unlock()
	it, ok := o.items[id]
	if !ok {
		return nil
	}
	return &Queued{
		Pending:     slices.Clone(it.rec.Pending),
		Attempts:    it.rec.Attempts,
		LastError:   it.rec.LastError,
		NextAttempt: it.next,
		GiveUp:      giveUp(id, maxAge),
		Handed:      slices.Clone(it.rec.Handed),
		CarriedFor:  it.rec.Carried,
		Parked:      parkedServers(it.rec.Parked),
	}
}

// giveUp is when a message queued under id stops being retried.
func giveUp(id string, maxAge time.Duration) time.Time {
	if maxAge <= 0 {
		return time.Time{}
	}
	at, ok := idTime(id)
	if !ok {
		return time.Time{}
	}
	return at.Add(maxAge)
}

// enqueue files msg in the Outbox for the recipients rec names, to be
// attempted at next. The record is written first, so a message is never
// left with no record.
func (mb *Mailbox) enqueue(msg []byte, rec queueRecord, next time.Time) (string, error) {
	mb.lim.storeMu.Lock()
	defer mb.lim.storeMu.Unlock()
	if err := mb.checkRoom(int64(len(msg))); err != nil {
		return "", err
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	o := mb.out
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.writeRecord(id, rec); err != nil {
		return "", err
	}
	if err := o.md.putAs(id, msg, true); err != nil {
		_ = os.Remove(o.recordPath(id)) //nolint:errcheck,gosec
		return "", err
	}
	o.items[id] = &queueItem{rec: rec, next: next}
	o.poke()
	return id, nil
}

// ErrNoOutbox answers Retry on a mailbox opened without an Outbox.
var ErrNoOutbox = errors.New("skymail: this mailbox has no outbox")

// Retry makes an Outbox message due now, or every one when id is empty.
func (mb *Mailbox) Retry(id string) error {
	if mb.out == nil {
		return ErrNoOutbox
	}
	if id == "" {
		mb.out.wake(cipher.PubKey{}, true, false)
		return nil
	}
	mb.out.mu.Lock()
	it, ok := mb.out.items[id]
	if ok {
		it.next, it.handNext, it.parkNext = time.Time{}, time.Time{}, time.Time{}
	}
	mb.out.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	mb.out.poke()
	return nil
}

// HasOutbox reports whether the mailbox was opened with an Outbox.
func (mb *Mailbox) HasOutbox() bool { return mb.out != nil }

// Queued is how many messages wait in the Outbox.
func (mb *Mailbox) Queued() int {
	if mb.out == nil {
		return 0
	}
	return mb.out.len()
}

// RunOutbox delivers queued mail until ctx ends: each message when its
// next attempt is due, and at once when Retry asks or its recipient has
// just delivered mail here, which shows that visor is back.
func (mb *Mailbox) RunOutbox(ctx context.Context) {
	if mb.out == nil {
		return
	}
	slots := make(chan struct{}, maxAttempts)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-mb.out.kick:
		}
		plan := mb.plan()
		for _, id := range mb.out.due(time.Now(), plan) {
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			go func() {
				defer func() { <-slots; mb.out.poke() }()
				mb.attempt(ctx, id, plan)
			}()
		}
		timer.Reset(mb.out.wait(time.Now(), plan))
	}
}

// attempt delivers one queued message if that is due, then offers it to
// the helpers that lack a copy if that is due.
func (mb *Mailbox) attempt(ctx context.Context, id string, plan outboxPlan) {
	st, ok := mb.out.snapshot(id)
	if !ok {
		return
	}
	rec := &st.rec
	msg, err := mb.out.md.read(id)
	if err != nil {
		mb.log.WithError(err).WithField("id", id).Warn("skymail: outbox message unreadable, dropped")
		_ = mb.out.drop(id) //nolint:errcheck
		return
	}
	if g := giveUp(id, mb.Limits().MaxAge); !g.IsZero() && !time.Now().Before(g) {
		reason := "gave up after " + FormatAge(mb.Limits().MaxAge)
		if rec.LastError != "" {
			reason += ": " + rec.LastError
		}
		for _, r := range rec.Pending {
			rec.Failed = append(rec.Failed, RcptResult{Rcpt: r, Err: reason})
		}
		rec.Pending = nil
		mb.finish(id, *rec, msg)
		return
	}

	if now := time.Now(); !st.next.After(now) {
		require := ""
		if rec.Carried != "" {
			// A visor that cannot open it would file the envelope as is.
			require = SealedExtension
		}
		actx, cancel := context.WithTimeout(ctx, attemptTimeout)
		groups, bad := groupRcpts(rec.Pending)
		results := mb.deliverGroups(actx, rec.From, groups, msg, require)
		cancel()
		if ctx.Err() != nil {
			// Shutting down is not the recipient's failure.
			_ = mb.out.release(id, nil) //nolint:errcheck
			return
		}
		rec.Attempts++
		rec.Failed = append(rec.Failed, bad...)
		rec.Pending = nil
		delivered := false
		for _, gr := range results {
			for _, r := range gr.g.rcpts {
				switch {
				case gr.err == nil:
					delivered = true
				case mb.retryable(gr.g, gr.err):
					rec.Pending = append(rec.Pending, r.Original)
					rec.LastError = gr.err.Error()
				default:
					rec.Failed = append(rec.Failed, RcptResult{Rcpt: r.Original, Err: gr.err.Error()})
				}
			}
		}
		if delivered && !rec.SentCopy && rec.Carried == "" {
			if _, err := mb.store(mb.sent, msg, true); err != nil {
				mb.log.WithError(err).Warn("skymail: keep Sent copy")
			}
			rec.SentCopy = true
		}
		if len(rec.Pending) == 0 {
			mb.finish(id, *rec, msg)
			return
		}
		st.next = time.Now().Add(retryDelay(rec.Attempts))
	}

	if st.needsHandoff(plan.helpers) && !st.handNext.After(time.Now()) {
		mb.handOff(ctx, &st, msg, plan.helpers)
	}
	if st.needsPark(plan.park) && !st.parkNext.After(time.Now()) {
		mb.park(ctx, &st, msg)
	}
	if err := mb.out.release(id, &st); err != nil {
		mb.log.WithError(err).WithField("id", id).Warn("skymail: outbox record")
	}
	mb.log.WithField("id", id).WithField("pending", len(rec.Pending)).WithField("attempts", rec.Attempts).
		WithField("handed", len(rec.Handed)).WithField("parked", len(rec.Parked)).WithField("next", st.next.Format(time.RFC3339)).
		Debug("skymail: still queued: " + rec.LastError)
}

// finish takes a message out of the Outbox once every recipient got it
// or failed for good, and tells the sender about the failures.
func (mb *Mailbox) finish(id string, rec queueRecord, msg []byte) {
	if err := mb.out.drop(id); err != nil {
		mb.log.WithError(err).WithField("id", id).Warn("skymail: clear outbox")
	}
	// Mail carried for another visor is not this visor's to bounce: its
	// sender keeps its own copy and its own retries.
	if len(rec.Failed) > 0 && rec.Carried == "" {
		mb.bounce(msg, rec.Failed)
	}
	mb.log.WithField("id", id).WithField("failed", len(rec.Failed)).Info("skymail: outbox message done")
}

// bounce files a delivery failure notice in the Inbox with the original
// attached, so nothing the sender wrote is lost with the Outbox copy.
func (mb *Mailbox) bounce(orig []byte, failed []RcptResult) {
	var subject, date, origFrom, msgID string
	if h, err := parseHeader(orig); err == nil {
		subject, date = decodeHeader(h.Get("Subject")), h.Get("Date")
		origFrom, msgID = h.Get("From"), h.Get("Message-ID")
	}
	var b strings.Builder
	b.WriteString("Your message could not be delivered to:\n\n")
	for _, f := range failed {
		fmt.Fprintf(&b, "  %s\n    %s\n", f.Rcpt, f.Err)
	}
	fmt.Fprintf(&b, "\nSubject: %s\nDate: %s\n\nThe original message is attached.\n", subject, date)

	daemon := "Mail Delivery System <" + mb.Address("mailer-daemon", Suffixes[0]) + ">"
	if origFrom == "" {
		origFrom = mb.Address("", Suffixes[0])
	}
	bounceID, err := newID()
	if err != nil {
		mb.log.WithError(err).Warn("skymail: bounce")
		return
	}
	notice := compose(daemon, Outgoing{
		To:        []string{origFrom},
		Subject:   "Undelivered mail: " + subject,
		Body:      b.String(),
		InReplyTo: msgID,
		Attachments: []OutgoingAttachment{{
			Name: "original.eml", ContentType: "application/octet-stream", Data: orig,
		}},
	}, "<"+bounceID+"@"+mb.cfg.PK.DNSLabel()+Suffixes[0]+">")
	// RFC 3834 §5: an automatic response, so no one auto-replies to it.
	notice = append([]byte("Auto-Submitted: auto-replied\r\n"), notice...)
	id, err := mb.deliverLocal(mb.cfg.PK, cipher.PubKey{}, notice)
	if err != nil {
		mb.log.WithError(err).Warn("skymail: file bounce")
		return
	}
	if mb.cfg.OnDeliver != nil {
		mb.cfg.OnDeliver(Delivered{ID: id, From: daemon, Subject: "Undelivered mail: " + subject, PeerPK: mb.cfg.PK})
	}
}

// retryable reports whether a failed delivery may work later: the peer
// could not be reached or answered 4xx (RFC 5321 §4.2.1). A 5xx answer,
// a suffix with no network here and the visor's own mailbox are final.
func (mb *Mailbox) retryable(g *peerGroup, err error) bool {
	if mb.out == nil || g.pk == mb.cfg.PK || mb.cfg.Dialers[g.suffix] == nil {
		return false
	}
	var te *textproto.Error
	if errors.As(err, &te) {
		return te.Code >= 400 && te.Code < 500
	}
	return true
}
