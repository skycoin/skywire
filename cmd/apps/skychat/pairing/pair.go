// Package pairing — cmd/apps/skychat/pairing/pair.go: a single chat
// pair's runtime state.
//
// A Pair wraps two CXO TreeStore endpoints under one struct:
//
//   - a Publisher exposing this side's outbox feed, with an allowlist
//     of exactly the peer's PK. The peer is the only entity permitted
//     to subscribe to and read this feed.
//   - a Subscriber connected to the peer's outbox feed, dialing the
//     same deterministic publisher port on the peer's PK.
//
// Both run on dedicated CXO nodes attached to a shared dmsg.Client.
// Within one visor, multiple pairs avoid DMSG-port collisions because
// each pair's publisher port is derived from the unique (a, b) hash
// and the subscriber port lives in a non-overlapping range
// (see ports.go).
//
// Lifecycle: Open creates the publisher (no subscriber yet) and is
// the act of "I'm ready to receive from this peer." Connect dials the
// peer's publisher and registers the inbound message callback. The
// two are split because the pairing handshake (PR-3) sends an invite
// after Open and only Connects after the peer's ack arrives.
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
// feed. Body encryption (PR-5) wraps Text into a ciphertext field;
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

	// DmsgC is the shared DMSG client. Both publisher and subscriber
	// CXO nodes attach to it.
	DmsgC *dmsg.Client

	// DataDir is the per-visor parent directory for CXO state. Each
	// Pair carves its own pub/<peer-pk-hex>/ and sub/<peer-pk-hex>/
	// subtrees inside it.
	DataDir string

	// Logger is optional; nil falls back to a tag-based default.
	Logger *logging.Logger

	// BatchWindow forwards to the publisher (see treestore.Config).
	BatchWindow time.Duration
}

// Pair is one live chat pair. Safe for concurrent use after Open
// returns; Send and Close may be called from any goroutine.
type Pair struct {
	cfg   Config
	ports PairPorts

	pub *treestore.Publisher
	sub *treestore.Subscriber

	// seq disambiguates messages with the same nanosecond timestamp.
	// Per-pair scope so a sender posting in tight loops doesn't
	// silently overwrite earlier leaves.
	seq atomic.Uint64

	handler MessageHandler

	log *logging.Logger
}

// Open constructs the pair's publisher with allowlist=[PeerPK] and
// records the deterministic ports. The subscriber is created but not
// yet connected — call Connect after the peer has acknowledged the
// pair invite (PR-3) or immediately when both sides are known to be
// ready.
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

	ports, err := ComputePairPorts(cfg.MyPK, cfg.PeerPK)
	if err != nil {
		return nil, fmt.Errorf("pairing: Open: compute ports: %w", err)
	}

	peerHex := cfg.PeerPK.Hex()
	pub, err := treestore.NewWithDMSG(cfg.DmsgC, cfg.MySK, treestore.PubConfig{
		BatchWindow:         cfg.BatchWindow,
		Logger:              log,
		DataDir:             filepath.Join(cfg.DataDir, "pub", peerHex),
		DmsgPort:            ports.Publisher,
		SubscriberAllowlist: []cipher.PubKey{cfg.PeerPK},
	})
	if err != nil {
		return nil, fmt.Errorf("pairing: Open: build publisher: %w", err)
	}

	sub, err := treestore.NewSubscriber(cfg.DmsgC, cfg.PeerPK, treestore.SubConfig{
		Logger:   log,
		DataDir:  filepath.Join(cfg.DataDir, "sub", peerHex),
		DmsgPort: ports.Subscriber,
	})
	if err != nil {
		_ = pub.Close() //nolint:errcheck
		return nil, fmt.Errorf("pairing: Open: build subscriber: %w", err)
	}
	sub.SetPrefixes([]string{MessagePathPrefix})

	p := &Pair{cfg: cfg, ports: ports, pub: pub, sub: sub, log: log}
	sub.OnUpdate(p.onUpdate)
	return p, nil
}

// Connect dials the peer's publisher and starts the subscribe
// handshake. Idempotent: returns nil if already connected.
//
// Open + Connect are split so the pairing handshake (PR-3) can stage
// the publisher before the peer is known to be ready, then activate
// the inbound side once the peer's pair-ack arrives.
func (p *Pair) Connect() error {
	if err := p.sub.Connect(p.cfg.PeerPK); err != nil {
		return fmt.Errorf("pairing: Connect: %w", err)
	}
	return nil
}

// Ports returns the publisher / subscriber DMSG ports this pair uses.
// Persisted by the manager so a future restart resumes on the same
// ports even if the deterministic computation later changes.
func (p *Pair) Ports() PairPorts {
	return p.ports
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

// Close tears down both endpoints. Idempotent.
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
			// Deletion — no inbound message to surface.
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
