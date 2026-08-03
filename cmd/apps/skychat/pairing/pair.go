// Package pairing cmd/apps/skychat/pairing/pair.go c4-app-chat
// pair's runtime state.
//
// One CXO node per pair side, listening on the deterministic pair
// port (ComputePairPort(my_pk, peer_pk)). The node hosts both roles:
//
//   - Publisher: shares this side's outbox feed, with an allowlist of
//     exactly the peer's PK. Only the peer may subscribe and read.
//   - Subscriber: connected to the peer's outbox feed (same port,
//     peer's PK as the feed identity). Receives messages from peer.
//
// One node per pair (not two) keeps the DMSG port allocation simple
// and matches the symmetric protocol: both ends listen on the same
// port number, both ends dial the other at that port. The CXO node
// multiplexes publisher and subscriber roles by feed PK.
//
// Lifecycle: Open creates the publisher (ready to accept the peer's
// subscribe) and constructs the subscriber attached to the publisher's
// node. Connect dials the peer's node and registers the inbound
// subscribe. Open + Connect are split so the pairing handshake (PR-5+)
// can stage the publisher before the peer is known to be ready.
package pairing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// MessagePathPrefix is the prefix used for message leaves within a
// pair feed. Subscribers set this on their TreeStore.Subscriber so
// future non-message metadata (e.g. typing indicators) can land
// under different prefixes without surfacing here.
const MessagePathPrefix = "msgs"

// announceRetryAfter bounds how often a pair re-attempts an
// announcement that failed to publish. Only consulted on Send, so this
// is a floor on retries, not a timer.
const announceRetryAfter = 30 * time.Second

// MessageTypeDelete marks a control record that retracts an earlier
// message rather than carrying one: Type is set, ID names the target
// (see Message.MsgID), and Text is empty.
//
// A peer running a build that predates this field decodes the record as
// a Message with no text and no Type, and nothing on that path filters
// empty text — so it won't apply the delete and may render a blank
// bubble. Handlers on this side skip any record with a non-empty Type,
// which is what keeps a future control type from doing the same to us.
const MessageTypeDelete = "delete"

// Message is the on-the-wire form of a chat message inside a pair
// feed. Body encryption (PR-6) wraps Text into a ciphertext field;
// for now Text is plaintext.
type Message struct {
	Text string    `json:"text"`
	TS   time.Time `json:"ts"`

	// Type is empty for a chat message, MessageTypeDelete for a
	// retraction. Kept omitempty so the common record is unchanged
	// on the wire.
	Type string `json:"type,omitempty"`

	// ID is the target of a MessageTypeDelete record — the MsgID of the
	// message being retracted. Unused (and omitted) on a chat message,
	// whose own id is derived from TS and Seq.
	ID string `json:"id,omitempty"`

	// Seq is the publisher's per-pair counter for this record, the same
	// one that disambiguates the leaf path. It's in the body so MsgID can
	// stay unique when two messages land on the same nanosecond — the
	// case the path's seq already exists to cover. Absent on records from
	// a peer that predates this field, which just means their ids end in
	// -0; both ends still derive the id from the same bytes, so they
	// agree either way.
	Seq uint64 `json:"seq,omitempty"`
}

// MsgID is the identifier both ends use to name this message. It is
// derived only from fields sealed into the body, so the peer computes
// the same string from the record it receives — which is what lets a
// delete published later name a message sent earlier.
//
// (timestamp, seq) is the same pair that keys the leaf path, so two
// messages can share an id only if they'd have collided on the feed too.
func (m Message) MsgID() string {
	return strconv.FormatInt(m.TS.UnixNano(), 10) + "-" + strconv.FormatUint(m.Seq, 10)
}

// MessageHandler is invoked on every newly-arrived peer message.
// Called from the subscriber's update goroutine; implementations
// should be non-blocking (queue the message and return).
type MessageHandler func(peerPK cipher.PubKey, msg Message)

// Config bundles the inputs Pair.Open needs.
type Config struct {
	// MyPK / MySK are this visor's identity.
	MyPK cipher.PubKey
	MySK cipher.SecKey

	// PeerPK is the chat partner.
	PeerPK cipher.PubKey

	// DmsgC is the shared DMSG client.
	DmsgC *dmsg.Client

	// DataDir is the per-visor parent directory for CXO state. Each
	// Pair carves its own pair/<peer-pk-hex>/ subtree inside it.
	DataDir string

	// Logger is optional; nil falls back to a tag-based default.
	Logger *logging.Logger

	// BatchWindow forwards to the publisher (see treestore.Config).
	BatchWindow time.Duration

	// Ratchet is the pair's persisted forward-secrecy state, restored
	// from the store. Nil for a brand-new pair (or one whose record
	// predates the ratchet), in which case Open mints a first keypair.
	Ratchet *RatchetState

	// OnRatchetChange is fired with the ratchet's persistable snapshot
	// whenever it advances, so the Manager can save it. Optional — a
	// Pair without one still works, it just re-mints its ratchet on
	// every restart and loses the epochs it had derived.
	OnRatchetChange func(RatchetState)
}

// Pair is one live chat pair. Safe for concurrent use after Open
// returns; Send and Close may be called from any goroutine.
type Pair struct {
	cfg  Config
	port uint16

	pub *treestore.Publisher
	sub *treestore.Subscriber

	// key is the LEGACY static ECDH key for this pair, derived from
	// (my SK, peer PK). Kept only to open leaves published before
	// either side had a ratchet, and to talk to a peer that never
	// announces one. Every new message goes out under an epoch key —
	// see ratchet below and the note in crypto.go.
	key pairKey

	// ratchet holds the short-lived keys that give this pair forward
	// secrecy: our current ratchet secret, the peer's announced public
	// key, and the ring of epoch keys that still open history.
	ratchet *ratchetState

	// onRatchetChange persists the ratchet after every advance.
	onRatchetChange func(RatchetState)

	// deferredMu / deferred hold leaves that named an epoch we could
	// not derive yet, kept until a ratchet announcement supplies the
	// key.
	//
	// This is not an optimisation, it is required for correctness. The
	// sender starts sealing under an epoch the moment IT can derive one
	// — which needs only OUR announcement — while we need THEIRS to
	// derive the same epoch, and those two arrive over independent
	// syncs. A one-second gap is normal, and Subscriber.OnUpdate fires
	// exactly once per leaf, so without this the messages sent in that
	// window are dropped permanently rather than late.
	deferredMu sync.Mutex
	deferred   []deferredLeaf

	// announceMu guards the announcement bookkeeping below.
	//
	// announcedGen is the ratchet generation whose announcement we have
	// successfully published. triedGen / lastAnnounceTry rate-limit
	// RETRIES of a generation whose publish failed, without holding back
	// a newer generation. All three are consulted only from Send — see
	// maybeAnnounce for why there is no background timer.
	announceMu      sync.Mutex
	announcedGen    uint64
	triedGen        uint64
	lastAnnounceTry time.Time

	// seq disambiguates messages with the same nanosecond timestamp.
	// Per-pair scope so a sender posting in tight loops doesn't
	// silently overwrite earlier leaves.
	seq atomic.Uint64

	handler MessageHandler

	log *logging.Logger
}

// Open constructs the pair's CXO node, brings up the publisher with
// allowlist=[PeerPK], and prepares a subscriber attached to the same
// node. The subscriber is not yet connected — call Connect after
// the peer is known to be ready (or immediately when both sides are
// brought up at once, e.g. in tests).
func Open(cfg Config) (*Pair, error) {
	if cfg.DmsgC == nil {
		return nil, errors.New("pairing: Open: DmsgC required")
	}
	if cfg.PeerPK == (cipher.PubKey{}) {
		return nil, errors.New("pairing: Open: PeerPK required")
	}
	if cfg.DataDir == "" {
		return nil, errors.New("pairing: Open: DataDir required")
	}
	log := cfg.Logger
	if log == nil {
		log = logging.MustGetLogger("skychat-pair")
	}

	port, err := ComputePairPort(cfg.MyPK, cfg.PeerPK)
	if err != nil {
		return nil, fmt.Errorf("pairing: Open: compute port: %w", err)
	}

	// Derive the pair's symmetric key now so a key-derivation
	// failure (e.g. invalid peer PK) is reported up front, before
	// we spin up any CXO nodes.
	key, err := derivePairKey(cfg.MySK, cfg.PeerPK)
	if err != nil {
		return nil, fmt.Errorf("pairing: Open: %w", err)
	}

	peerHex := cfg.PeerPK.Hex()
	pub, err := treestore.NewWithDMSG(cfg.DmsgC, cfg.MySK, treestore.PubConfig{
		BatchWindow:         cfg.BatchWindow,
		Logger:              log,
		DataDir:             filepath.Join(cfg.DataDir, "pair", peerHex),
		DmsgPort:            port,
		SubscriberAllowlist: []cipher.PubKey{cfg.PeerPK},
		// Pair messages are content-addressed and replayed on rejoin
		// from the persistent pair store — losing a torn write at
		// crash time costs at most one batch of un-acked messages,
		// recoverable on the next sync.
		NoSyncCXDS: true,
	})
	if err != nil {
		return nil, fmt.Errorf("pairing: Open: build publisher: %w", err)
	}

	// Subscriber piggybacks on the publisher's CXO node. One node
	// per pair side hosts both roles, addressed by feed PK.
	sub, err := treestore.NewSubscriberOnNode(pub.Node(), cfg.PeerPK, treestore.SubConfig{
		Logger: log,
	})
	if err != nil {
		_ = pub.Close() //nolint:errcheck
		return nil, fmt.Errorf("pairing: Open: attach subscriber: %w", err)
	}
	// Both prefixes: msgs/ carries content, ratchet/ carries the peer's
	// announced ratchet keys. Without the second the peer's
	// announcements would never reach onUpdate and the pair would sit
	// on the legacy static key forever.
	sub.SetPrefixes([]string{MessagePathPrefix, RatchetPathPrefix})

	now := time.Now().UTC()
	var rt *ratchetState
	if cfg.Ratchet != nil {
		rt = restoreRatchetState(*cfg.Ratchet, now)
	} else {
		rt = newRatchetState(now)
	}

	p := &Pair{
		cfg: cfg, port: port, pub: pub, sub: sub, key: key, log: log,
		ratchet:         rt,
		onRatchetChange: cfg.OnRatchetChange,
	}
	sub.OnUpdate(p.onUpdate)

	// Persist the freshly-minted (or restored) ratchet right away.
	// Without this a brand-new pair holds its generation-1 secret only
	// in memory until something else advances the ratchet, so a restart
	// before the peer's first announcement mints generation 1 AGAIN
	// under a different secret — and the announcement the peer already
	// fetched names a public key we no longer hold the other half of.
	p.saveRatchet()

	// No announcement here, and no background timer to publish one
	// either — Send does it. See maybeAnnounce.
	return p, nil
}

// maybeAnnounce publishes our ratchet announcement, and rotates first if
// the current secret has covered enough, as part of whatever publish the
// caller is about to make.
//
// # Why this hangs off Send instead of a timer
//
// The announcement is a CXO write, and pkg/cxo/skyobject HAD a
// lock-order inversion between a write and a fill on the SAME node:
// Cache.Get held Cache.mx and blocked on bbolt's writer lock, while
// Cache.WithBatch (reached only from the publisher's tree walk) held
// bbolt's writer lock and blocked on Cache.mx. A pair puts its publisher
// and its subscriber on one node by design, so an announcement
// published while the peer's tree was filling could wedge the node
// permanently — the publish loop stops clearing its dirty flag and
// every later send is silently lost.
//
// Confirmed rather than guessed: with Cache.WithBatch bypassed the
// pairing suite ran 6/6 green, and with it a background announce timer
// hung the suite roughly one run in four.
//
// That inversion is fixed — WithBatch now takes Cache.mx before the
// writer, pinned by TestWithBatch_DoesNotInvertCacheAndWriterLocks — so
// a timer is no longer unsafe. Riding Send is kept anyway, on its own
// merits: the Put lands in the same publisher batch as the message, so
// the pair performs exactly as many publishes as it did before this
// feature existed, and no timing assumption is involved at all. The
// cost is that forward secrecy engages once each side has sent at least
// once (the first message from each side goes out under the legacy
// static key), which for a conversation is the normal case. A timer
// would only add value for pairs where one side never speaks.
func (p *Pair) maybeAnnounce(now time.Time) {
	if p.pub == nil {
		return
	}
	if p.ratchet.rotationDue(now) {
		gen := p.ratchet.rotate(now)
		p.log.WithField("peer", p.cfg.PeerPK.Hex()).WithField("generation", gen).
			Debug("pairing: rotated ratchet key")
		p.saveRatchet()
	}
	gen := p.ratchet.generation()

	p.announceMu.Lock()
	switch {
	case p.announcedGen == gen:
		// Already out. Re-publishing the same leaf would be a no-op
		// write on every send.
		p.announceMu.Unlock()
		return
	case p.triedGen == gen && now.Sub(p.lastAnnounceTry) < announceRetryAfter:
		// A failed attempt for THIS generation, too soon to retry.
		//
		// Scoped to the generation on purpose: a fresh rotation must go
		// out immediately, not wait out a backoff earned by the
		// previous one. Until the peer sees the new ratchet key it
		// cannot derive the new epoch, and everything we send under it
		// piles up in the peer's deferred list.
		p.announceMu.Unlock()
		return
	}
	p.triedGen, p.lastAnnounceTry = gen, now
	p.announceMu.Unlock()

	if p.publishRatchet() {
		p.announceMu.Lock()
		p.announcedGen = gen
		p.announceMu.Unlock()
	}
}

// publishRatchet signs and publishes our current ratchet announcement.
//
// Idempotent on the path (ratchet/<generation>), so re-publishing the
// same generation overwrites an identical leaf rather than growing the
// tree. Best-effort: a publisher that isn't ready yet gets retried by the
// announce loop, and until then the pair keeps working on the legacy key.
func (p *Pair) publishRatchet() bool {
	if p.pub == nil {
		return false
	}
	a, err := p.ratchet.announce(p.cfg.MySK, time.Now().UTC())
	if err != nil {
		p.log.WithError(err).Debug("pairing: could not sign ratchet announcement")
		return false
	}
	body, err := marshalRatchet(a)
	if err != nil {
		p.log.WithError(err).Debug("pairing: could not marshal ratchet announcement")
		return false
	}
	path := RatchetPathPrefix + "/" + strconv.FormatUint(a.Generation, 10)
	if err := p.pub.Put(path, body); err != nil {
		p.log.WithError(err).WithField("path", path).
			Debug("pairing: could not publish ratchet announcement; will retry on the next send")
		return false
	}
	return true
}

// saveRatchet hands the ratchet's current snapshot to the persistence
// callback.
func (p *Pair) saveRatchet() {
	if p.onRatchetChange == nil {
		return
	}
	p.onRatchetChange(p.ratchet.snapshot())
}

// EpochID returns the epoch this pair currently seals under, and false
// when it is still on the legacy static key.
//
// False is the normal state for a conversation nobody has spoken in
// yet: an epoch needs BOTH ratchet keys, each side publishes its own as
// part of a Send (see maybeAnnounce), so the epoch appears once each end
// has sent at least once. It also stays false against a peer running a
// build with no ratchet at all.
//
// Operator-facing: the UI shows it so a user can tell which of the two
// modes a conversation is in, since they are otherwise indistinguishable
// and differ in exactly the property that matters here.
func (p *Pair) EpochID() (EpochID, bool) {
	id, _, ok := p.ratchet.currentEpoch()
	return id, ok
}

// Connect dials the peer's CXO node and starts the subscribe
// handshake. Idempotent: returns nil if already connected.
//
// ctx bounds the dmsg dial — see Subscriber.Connect. Callers without
// a natural ctx can pass context.Background(); the dial will then
// rely on dmsg's own timeouts to escape a hung peer.
//
// Open + Connect are split so the pairing handshake can stage the
// publisher before the peer is known to be ready, then activate the
// inbound side once the peer's pair-ack arrives.
//
// Returns ErrPairClosed if Close has already torn down the
// subscriber. Pre-fix this nil-deref'd on p.sub.Connect.
func (p *Pair) Connect(ctx context.Context) error {
	if p.sub == nil {
		return ErrPairClosed
	}
	if err := p.sub.Connect(ctx, p.cfg.PeerPK); err != nil {
		return fmt.Errorf("pairing: Connect: %w", err)
	}
	return nil
}

// Port returns the deterministic DMSG port this pair uses on both sides.
func (p *Pair) Port() uint16 {
	return p.port
}

// PeerPK is a convenience accessor.
func (p *Pair) PeerPK() cipher.PubKey {
	return p.cfg.PeerPK
}

// SetMessageHandler registers (or replaces) the inbound-message
// callback. Pass nil to unregister.
func (p *Pair) SetMessageHandler(h MessageHandler) {
	p.handler = h
}

// ErrPairClosed is returned by Send / Connect when the pair has
// already been Closed. Surfaces the post-close state as a clean
// error instead of a nil-pointer panic on p.pub / p.sub. Sentinel
// so callers can distinguish closed-pair from transient publisher
// errors via errors.Is(err, ErrPairClosed).
var ErrPairClosed = errors.New("pairing: pair is closed")

// Send publishes a message to this side's outbox feed. The peer's
// subscriber will see it on the next CXO publish-batch cycle.
//
// The message body is sealed with the pair's ECDH-derived key
// before being stored as the leaf value, so anyone who breaches
// the publisher allowlist still cannot read message content.
// The path itself (timestamp + seq) stays plaintext.
//
// Returns ErrPairClosed if Close has already torn down the
// publisher. Pre-fix this nil-deref'd on p.pub.Put.
func (p *Pair) Send(text string) error {
	_, err := p.SendID(text)
	return err
}

// SendID publishes text like Send and returns the new message's MsgID.
// Callers that may later retract the message need this: the id is minted
// here from the timestamp that goes into the sealed body, so it is the
// only value that names the same message on both sides.
func (p *Pair) SendID(text string) (string, error) {
	return p.publish(Message{Text: text})
}

// SendDelete retracts an earlier message by publishing a delete record
// naming its id. The record rides the same feed as the message it
// retracts, so it is durable and ordered: a peer that is offline now
// applies the delete when it next syncs, and a peer that has not yet
// received the message gets both in the same batch.
//
// No authorship check is possible or needed — a pair feed is
// single-writer, so this can only ever retract our own messages.
func (p *Pair) SendDelete(id string) error {
	if id == "" {
		return errors.New("pairing: SendDelete: empty id")
	}
	_, err := p.publish(Message{Type: MessageTypeDelete, ID: id})
	return err
}

// publish stamps msg with the current time, seals it and puts it on the
// outbox feed, returning the record's own MsgID. Shared by SendID and
// SendDelete so both go through the same announce/seal/put sequence.
func (p *Pair) publish(msg Message) (string, error) {
	if p.pub == nil {
		return "", ErrPairClosed
	}
	now := time.Now().UTC()
	// Announce (and rotate, if due) FIRST, so the ratchet leaf and this
	// message coalesce into the same publisher batch — one publish, not
	// two. See maybeAnnounce.
	p.maybeAnnounce(now)

	// Take the seq before sealing: it goes into the body (so both ends
	// derive the same MsgID) and into the leaf path, and the two must be
	// the same number.
	seq := p.seq.Add(1)
	msg.TS, msg.Seq = now, seq
	body, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("pairing: Send: marshal: %w", err)
	}
	// Epoch key when we have one, legacy static key when we don't. The
	// fallback fires only before the peer's first announcement reaches
	// us — never as a downgrade afterwards, since currentEpoch stays
	// set once an epoch exists.
	var sealed []byte
	if id, key, ok := p.ratchet.currentEpoch(); ok {
		sealed, err = sealEnvelope(id, key, body)
		if err != nil {
			return "", fmt.Errorf("pairing: Send: %w", err)
		}
		p.ratchet.noteSent()
	} else {
		sealed, err = sealMessage(p.key, body)
		if err != nil {
			return "", fmt.Errorf("pairing: Send: %w", err)
		}
	}
	path := MessagePathPrefix + "/" + strconv.FormatInt(now.UnixNano(), 10) + "/" + strconv.FormatUint(seq, 10)
	if err := p.pub.Put(path, sealed); err != nil {
		return "", fmt.Errorf("pairing: Send: put %q: %w", path, err)
	}
	return msg.MsgID(), nil
}

// Close tears down the subscriber + publisher (and the underlying
// CXO node, owned by the publisher). Idempotent.
func (p *Pair) Close() error {
	var firstErr error
	if p.sub != nil {
		if err := p.sub.Close(); err != nil {
			firstErr = err
		}
		p.sub = nil
	}
	if p.pub != nil {
		if err := p.pub.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		p.pub = nil
	}
	return firstErr
}

// onUpdate is the subscriber callback. Opens each leaf with the
// pair's ECDH key and decodes the resulting plaintext as a Message,
// then dispatches via the registered handler. Tolerates non-msg
// leaves (e.g. future typing indicators under a different prefix)
// or tampered/foreign ciphertexts by silently dropping them — a
// failure here would surface as a noisy log without giving the
// user any actionable signal.
func (p *Pair) onUpdate(events []treestore.UpdateEvent) {
	for _, ev := range events {
		if ev.Value == nil {
			continue
		}
		// Ratchet announcements first, and BEFORE any message in the
		// same batch is opened: a resync delivers the peer's
		// announcement and the messages sealed under the epoch it forms
		// in one callback, and handling them in path order would drop
		// every one of those messages as undecryptable.
		if strings.HasPrefix(ev.Path, RatchetPathPrefix+"/") {
			p.applyRatchetLeaf(ev.Value)
		}
	}
	h := p.handler
	if h == nil {
		return
	}
	for _, ev := range events {
		if ev.Value == nil || strings.HasPrefix(ev.Path, RatchetPathPrefix+"/") {
			continue
		}
		p.deliverLeaf(h, ev.Path, ev.Value)
	}
}

// deliverLeaf opens one message leaf and hands it to the handler.
//
// A leaf that names an epoch we cannot derive YET is parked rather than
// dropped — see the deferred field. Anything else (tampered bytes, a
// foreign ciphertext, undecodable JSON) is dropped, because no future
// event would change the outcome.
func (p *Pair) deliverLeaf(h MessageHandler, path string, leaf []byte) {
	plaintext, err := p.openLeaf(leaf)
	if err != nil {
		if id, _, tagged := parseEnvelope(leaf); tagged {
			if _, held := p.ratchet.keyFor(id); !held {
				p.deferLeaf(path, leaf)
				p.log.WithField("path", path).WithField("epoch", id.String()).
					Debug("pairing: parked a leaf until its epoch key arrives")
				return
			}
		}
		p.log.WithError(err).WithField("path", path).
			Debug("pairing: ignoring undecryptable leaf")
		return
	}
	var msg Message
	if err := json.Unmarshal(plaintext, &msg); err != nil {
		p.log.WithError(err).WithField("path", path).
			Debug("pairing: ignoring undecodable leaf")
		return
	}
	h(p.cfg.PeerPK, msg)
}

// deferredLeaf is one parked message plus the path it came from, which
// doubles as its dedup key.
type deferredLeaf struct {
	path string
	body []byte
}

// maxDeferredLeaves bounds the park. A peer that publishes leaves under
// epochs it never announces would otherwise grow this without limit, and
// the window this exists to cover is seconds — anything still parked
// after hundreds of messages is not going to be resolved by waiting.
const maxDeferredLeaves = 256

func (p *Pair) deferLeaf(path string, body []byte) {
	p.deferredMu.Lock()
	defer p.deferredMu.Unlock()
	for _, d := range p.deferred {
		if d.path == path {
			return
		}
	}
	p.deferred = append(p.deferred, deferredLeaf{path: path, body: append([]byte(nil), body...)})
	if len(p.deferred) > maxDeferredLeaves {
		p.deferred = p.deferred[len(p.deferred)-maxDeferredLeaves:]
	}
}

// drainDeferred re-attempts every parked leaf, delivering the ones whose
// epoch key has since arrived and keeping the rest parked.
//
// Called after a ratchet advance, which is the only event that can add a
// key — so this never spins on leaves that are simply undeliverable.
func (p *Pair) drainDeferred() {
	h := p.handler
	if h == nil {
		return
	}
	p.deferredMu.Lock()
	pending := p.deferred
	p.deferred = nil
	p.deferredMu.Unlock()

	for _, d := range pending {
		p.deliverLeaf(h, d.path, d.body)
	}
}

// openLeaf decrypts one message leaf, routing by wire form: an
// epoch-tagged envelope goes to the ring, an untagged blob to the legacy
// static key.
//
// An envelope naming an epoch we no longer hold is a real, expected
// outcome — the ring is bounded on purpose (ratchetRingCap), so history
// past the horizon stops opening. The error says so, because "message
// too old to decrypt" and "message corrupt" are very different things to
// see in a log.
func (p *Pair) openLeaf(leaf []byte) ([]byte, error) {
	id, body, tagged := parseEnvelope(leaf)
	if !tagged {
		return openMessage(p.key, leaf)
	}
	key, ok := p.ratchet.keyFor(id)
	if !ok {
		return nil, fmt.Errorf("pairing: no key for epoch %s (rotated out of the ring, or not yet derived)", id)
	}
	return openMessage(key, body)
}

// applyRatchetLeaf verifies a peer announcement and folds it into our
// ratchet.
//
// Every accepted announcement is persisted, including ones that don't
// advance the current epoch: an older generation still DERIVES an epoch
// key we may need for messages already in the feed, and losing that key
// on restart would make those messages permanently unreadable.
func (p *Pair) applyRatchetLeaf(body []byte) {
	a, err := unmarshalRatchet(body, p.cfg.PeerPK)
	if err != nil {
		p.log.WithError(err).Debug("pairing: ignoring invalid ratchet announcement")
		return
	}
	advanced, err := p.ratchet.observePeer(a, time.Now().UTC())
	if err != nil {
		p.log.WithError(err).WithField("generation", a.Generation).
			Debug("pairing: could not derive an epoch from the peer's ratchet key")
		return
	}
	p.saveRatchet()
	// A new key just landed, so retry anything parked for want of one.
	// Unconditional rather than gated on `advanced`: an OLDER
	// announcement still derives an epoch key (see observePeer), and
	// the leaves waiting on it are exactly the ones an out-of-order
	// sync produces.
	p.drainDeferred()
	if advanced {
		id, _ := p.EpochID()
		p.log.WithField("peer", p.cfg.PeerPK.Hex()).
			WithField("generation", a.Generation).WithField("epoch", id.String()).
			Debug("pairing: advanced to a new epoch")
	}
}
