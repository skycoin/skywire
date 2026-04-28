// Package pairing — cmd/apps/skychat/pairing/pair.go: a single chat
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
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
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

// Message is the on-the-wire form of a chat message inside a pair
// feed. Body encryption (PR-6) wraps Text into a ciphertext field;
// for now Text is plaintext.
type Message struct {
	Text string    `json:"text"`
	TS   time.Time `json:"ts"`
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
}

// Pair is one live chat pair. Safe for concurrent use after Open
// returns; Send and Close may be called from any goroutine.
type Pair struct {
	cfg  Config
	port uint16

	pub *treestore.Publisher
	sub *treestore.Subscriber

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

	peerHex := cfg.PeerPK.Hex()
	pub, err := treestore.NewWithDMSG(cfg.DmsgC, cfg.MySK, treestore.PubConfig{
		BatchWindow:         cfg.BatchWindow,
		Logger:              log,
		DataDir:             filepath.Join(cfg.DataDir, "pair", peerHex),
		DmsgPort:            port,
		SubscriberAllowlist: []cipher.PubKey{cfg.PeerPK},
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
	sub.SetPrefixes([]string{MessagePathPrefix})

	p := &Pair{cfg: cfg, port: port, pub: pub, sub: sub, log: log}
	sub.OnUpdate(p.onUpdate)
	return p, nil
}

// Connect dials the peer's CXO node and starts the subscribe
// handshake. Idempotent: returns nil if already connected.
//
// Open + Connect are split so the pairing handshake can stage the
// publisher before the peer is known to be ready, then activate the
// inbound side once the peer's pair-ack arrives.
func (p *Pair) Connect() error {
	if err := p.sub.Connect(p.cfg.PeerPK); err != nil {
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

// Send publishes a message to this side's outbox feed. The peer's
// subscriber will see it on the next CXO publish-batch cycle.
func (p *Pair) Send(text string) error {
	now := time.Now().UTC()
	msg := Message{Text: text, TS: now}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("pairing: Send: marshal: %w", err)
	}
	seq := p.seq.Add(1)
	path := MessagePathPrefix + "/" + strconv.FormatInt(now.UnixNano(), 10) + "/" + strconv.FormatUint(seq, 10)
	if err := p.pub.Put(path, body); err != nil {
		return fmt.Errorf("pairing: Send: put %q: %w", path, err)
	}
	return nil
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

// onUpdate is the subscriber callback. Decodes each leaf as a Message
// and dispatches via the registered handler. Tolerates non-msg leaves
// (e.g. future typing indicators under a different prefix) by
// silently ignoring decode failures.
func (p *Pair) onUpdate(events []treestore.UpdateEvent) {
	h := p.handler
	if h == nil {
		return
	}
	for _, ev := range events {
		if ev.Value == nil {
			continue
		}
		var msg Message
		if err := json.Unmarshal(ev.Value, &msg); err != nil {
			p.log.WithError(err).
				WithField("path", ev.Path).
				Debug("pairing: ignoring undecodable leaf")
			continue
		}
		h(p.cfg.PeerPK, msg)
	}
}
