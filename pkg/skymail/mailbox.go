// Package skymail pkg/skymail/mailbox.go c4-app-mail
package skymail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skymailbridge"
)

// Suffixes are the address TLDs a mailbox answers to. Both name the
// same PK; they differ only in which network a sender dials.
var Suffixes = []string{".skynet", ".dmsg"}

// PeerHeader carries the sender PK the receiving visor authenticated.
// The mailbox writes it at the top of every message it accepts, so the
// first occurrence is always its own; one a sender forged sits below.
const PeerHeader = "X-Skymail-Peer"

// RelayHeader names the visor that delivered a sealed envelope for its
// signer. The mailbox writes it on the line after PeerHeader, and only
// a header in that place counts.
const RelayHeader = "X-Skymail-Relay"

// Config is what a Mailbox needs from its host.
type Config struct {
	// Dir is the Maildir root. Inbox lives here, Sent in Dir/.Sent.
	Dir string
	// PK is the mailbox owner: mail is accepted only for <x>@<PK>.
	PK cipher.PubKey
	// PeerPK names the authenticated remote PK of an inbound
	// connection. The visor supplies it (dmsg and skynet addresses
	// differ); ok=false means the caller cannot be identified.
	PeerPK func(net.Conn) (cipher.PubKey, bool)
	// Dialers reach a peer's port 25, keyed by address suffix. A
	// suffix with no dialer cannot be sent to.
	Dialers map[string]skymailbridge.Dialer
	// Limits bound what the mailbox keeps; zero fields take defaults.
	Limits Limits
	// Outbox queues mail for a recipient that cannot take it yet and
	// retries it (RunOutbox). Off, such a send fails at once.
	Outbox bool
	// Sealer, when set, seals and opens envelopes as PK. The mailbox then
	// opens sealed envelopes (sealed.go) that any visor delivers, and files
	// them under the PK that signed them.
	Sealer Sealer
	// Helpers, with Sealer and the Outbox, names the visors that get a
	// sealed copy of queued mail to deliver too (helpers.go).
	Helpers func() []cipher.PubKey
	// RelayFor, with Sealer and the Outbox, reports whether this mailbox
	// carries sealed mail that pk hands over for someone else.
	RelayFor func(pk cipher.PubKey) bool
	// OnDeliver, when set, is told of each message the network files in the
	// inbox, once it is stored. It must not block.
	OnDeliver func(Delivered)
	Log       logrus.FieldLogger
}

// Delivered is a message the network just filed in the inbox.
type Delivered struct {
	ID      string
	From    string
	Subject string
	PeerPK  cipher.PubKey
}

// Mailbox is one PK's mail: a Maildir, an SMTP receiver policy, and a
// sender. Safe for concurrent use.
type Mailbox struct {
	cfg   Config
	inbox *maildir
	sent  *maildir
	out   *outbox
	seen  *seenIndex
	log   logrus.FieldLogger

	wlMu      sync.RWMutex
	whitelist map[cipher.PubKey]struct{}

	lim limitState
}

// Open creates (or reopens) the mailbox under cfg.Dir.
func Open(cfg Config) (*Mailbox, error) {
	if cfg.Dir == "" {
		return nil, errors.New("skymail: Dir is empty")
	}
	if cfg.PK.Null() {
		return nil, errors.New("skymail: PK is empty")
	}
	inbox, err := openMaildir(cfg.Dir)
	if err != nil {
		return nil, err
	}
	sent, err := openMaildir(filepath.Join(cfg.Dir, "."+FolderSent))
	if err != nil {
		return nil, err
	}
	var out *outbox
	if cfg.Outbox {
		if out, err = openOutbox(cfg.Dir); err != nil {
			return nil, err
		}
	}
	log := cfg.Log
	if log == nil {
		log = logrus.NewEntry(logrus.New())
	}
	seen, err := openSeen(cfg.Dir)
	if err != nil {
		return nil, err
	}
	mb := &Mailbox{cfg: cfg, inbox: inbox, sent: sent, out: out, seen: seen, log: log}
	mb.SetLimits(cfg.Limits)
	if err := mb.loadWhitelist(); err != nil {
		return nil, err
	}
	return mb, nil
}

// Address returns the mailbox's address for local part and suffix,
// e.g. "me@<base32-pk>.skynet".
func (mb *Mailbox) Address(local, suffix string) string {
	if local == "" {
		local = "mail"
	}
	if suffix == "" {
		suffix = Suffixes[0]
	}
	return local + "@" + mb.cfg.PK.DNSLabel() + suffix
}

// whitelistFile holds the whitelist inside the mailbox directory, one
// hex PK per line, so it persists wherever the mail does (on js/wasm,
// in the IndexedDB snapshot) with no config write.
const whitelistFile = "whitelist"

func (mb *Mailbox) loadWhitelist() error {
	raw, err := os.ReadFile(filepath.Join(mb.cfg.Dir, whitelistFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var pks []cipher.PubKey
	for _, line := range strings.Fields(string(raw)) {
		var pk cipher.PubKey
		if err := pk.Set(line); err != nil {
			return fmt.Errorf("skymail: whitelist entry %q: %w", line, err)
		}
		pks = append(pks, pk)
	}
	mb.setWhitelist(pks)
	return nil
}

// SetWhitelist replaces the set of PKs allowed to deliver and saves it.
// An empty set accepts mail from any PK, as an unrestricted `serve`
// port does.
func (mb *Mailbox) SetWhitelist(pks []cipher.PubKey) error {
	var b strings.Builder
	for _, pk := range pks {
		b.WriteString(pk.Hex())
		b.WriteByte('\n')
	}
	path := filepath.Join(mb.cfg.Dir, whitelistFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("skymail: save whitelist: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("skymail: save whitelist: %w", err)
	}
	mb.setWhitelist(pks)
	return nil
}

func (mb *Mailbox) setWhitelist(pks []cipher.PubKey) {
	wl := make(map[cipher.PubKey]struct{}, len(pks))
	for _, pk := range pks {
		wl[pk] = struct{}{}
	}
	mb.wlMu.Lock()
	mb.whitelist = wl
	mb.wlMu.Unlock()
}

// Whitelist returns the PKs allowed to deliver; empty means everyone.
func (mb *Mailbox) Whitelist() []cipher.PubKey {
	mb.wlMu.RLock()
	defer mb.wlMu.RUnlock()
	out := make([]cipher.PubKey, 0, len(mb.whitelist))
	for pk := range mb.whitelist {
		out = append(out, pk)
	}
	return out
}

func (mb *Mailbox) allowed(pk cipher.PubKey) bool {
	mb.wlMu.RLock()
	defer mb.wlMu.RUnlock()
	if len(mb.whitelist) == 0 {
		return true
	}
	_, ok := mb.whitelist[pk]
	return ok
}

// isLocal reports whether addr is a mailbox address of this PK.
func (mb *Mailbox) isLocal(addr string) (bool, error) {
	for _, sfx := range Suffixes {
		pk, _, ok, err := skymailbridge.ParseRecipient(addr, sfx, "a")
		if err != nil {
			return false, err
		}
		if ok {
			return pk == mb.cfg.PK, nil
		}
	}
	return false, nil
}

// Serve accepts SMTP on lis until ctx ends or lis closes.
func (mb *Mailbox) Serve(ctx context.Context, lis net.Listener) error {
	return skymailbridge.ServeHandler(ctx, lis, receiver{mb}, mb.cfg.PK.DNSLabel()+Suffixes[0], mb.log)
}

// receiver is the Mailbox's SMTP policy, kept off Mailbox's own method
// set so Rcpt/Deliver read as SMTP verbs, not mailbox operations.
type receiver struct{ mb *Mailbox }

// MaxMessageSize makes the session enforce the mailbox's own limit.
func (r receiver) MaxMessageSize() int64 { return r.mb.MaxMessageSize() }

// MaxDataSize lets in a sealed envelope that holds a message of the
// largest size. Deliver checks what the body holds.
func (r receiver) MaxDataSize() int64 {
	if n := r.mb.MaxMessageSize(); r.mb.sealing() && n > 0 {
		return SealedSize(n)
	}
	return r.mb.MaxMessageSize()
}

// Extensions tells senders this mailbox opens sealed envelopes.
func (r receiver) Extensions() []string {
	if r.mb.sealing() {
		return []string{SealedExtension}
	}
	return nil
}

var errNotWhitelisted = &skymailbridge.Reply{Code: 550, Enhanced: "5.7.1", Text: "sender PK not whitelisted by this mailbox"}

var errOneVisor = &skymailbridge.Reply{Code: 452, Enhanced: "4.5.3", Text: "one recipient visor per message here; send the rest separately"}

func (r receiver) Rcpt(c net.Conn, from, rcpt string, accepted []string) error {
	peer, ok := r.mb.peerPK(c)
	if !ok {
		return errNotWhitelisted
	}
	local, err := r.mb.isLocal(rcpt)
	if err != nil {
		return &skymailbridge.Reply{Code: 550, Enhanced: "5.1.3", Text: err.Error()}
	}
	if !local {
		// A mailbox, and a carrier of sealed mail only for the visors it
		// relays for. An envelope opens for one visor, so a carried
		// message has one.
		pk, ok := addressPK(rcpt)
		if !ok || !r.mb.relays(peer) {
			return &skymailbridge.Reply{Code: 550, Enhanced: "5.7.1", Text: rcpt + " is not a mailbox of this visor"}
		}
		for _, a := range accepted {
			if apk, _ := addressPK(a); apk != pk {
				return errOneVisor
			}
		}
		if r.mb.full() {
			return &skymailbridge.Reply{Code: 452, Enhanced: "4.2.2", Text: "mailbox full"}
		}
		return nil
	}
	for _, a := range accepted {
		if apk, _ := addressPK(a); apk != r.mb.cfg.PK {
			return errOneVisor
		}
	}
	if !r.mb.allowed(peer) {
		// A relay may carry a sealed envelope from a sender this mailbox
		// takes mail from. Deliver checks the signature.
		claimed, cok := addressPK(from)
		if !r.mb.sealing() || !cok || !r.mb.allowed(claimed) {
			return errNotWhitelisted
		}
	}
	if r.mb.full() {
		return &skymailbridge.Reply{Code: 452, Enhanced: "4.2.2", Text: "mailbox full"}
	}
	// The peer just reached us, so mail waiting for it may get through.
	r.mb.out.wake(peer, false, slices.Contains(r.mb.helpers(), peer))
	return nil
}

func (r receiver) Deliver(_ context.Context, env skymailbridge.Envelope) (string, error) {
	peer, _ := r.mb.peerPK(env.Conn)
	if len(env.Rcpts) > 0 {
		// Rcpt took only local recipients or only carried ones.
		if local, err := r.mb.isLocal(env.Rcpts[0]); err == nil && !local {
			return r.mb.carry(peer, env)
		}
	}
	sender, relay, body := peer, cipher.PubKey{}, env.Body
	if r.mb.sealing() && IsSealed(body) {
		msg, signer, err := r.mb.cfg.Sealer.Open(body)
		if err != nil {
			return "", &skymailbridge.Reply{Code: 554, Enhanced: "5.7.5", Text: err.Error()}
		}
		sender, body = signer, msg
		if peer != signer {
			relay = peer
		}
	}
	if !r.mb.allowed(sender) {
		return "", errNotWhitelisted
	}
	if limit := r.mb.MaxMessageSize(); limit > 0 && int64(len(body)) > limit {
		return "", &skymailbridge.Reply{Code: 552, Enhanced: "5.3.4", Text: fmt.Sprintf("message exceeds the limit of %d octets", limit)}
	}
	key := seenKey(sender, body)
	if key != "" && !r.mb.seen.claim(key, time.Now()) {
		r.mb.log.WithField("from_pk", sender.Hex()).WithField("relay_pk", relay.Hex()).Debug("skymail: repeat dropped")
		return "Ok: already delivered", nil
	}
	id, err := r.mb.deliverLocal(sender, relay, body)
	if key != "" {
		if err != nil {
			r.mb.seen.release(key)
		} else if kerr := r.mb.seen.keep(key); kerr != nil {
			r.mb.log.WithError(kerr).Warn("skymail: remember delivered message")
		}
	}
	if errors.Is(err, ErrMailboxFull) {
		return "", &skymailbridge.Reply{Code: 452, Enhanced: "4.2.2", Text: "mailbox full"}
	}
	if err != nil {
		r.mb.log.WithError(err).Warn("skymail: store")
		return "", &skymailbridge.Reply{Code: 451, Enhanced: "4.3.0", Text: "mailbox store failed"}
	}
	log := r.mb.log.WithField("from_pk", sender.Hex()).WithField("id", id).WithField("bytes", len(body))
	if !relay.Null() {
		log = log.WithField("relay_pk", relay.Hex())
	}
	log.Info("skymail: delivered")
	if r.mb.cfg.OnDeliver != nil {
		d := Delivered{ID: id, PeerPK: sender}
		if h, err := parseHeader(body); err == nil {
			d.From, d.Subject = decodeHeader(h.Get("From")), decodeHeader(h.Get("Subject"))
		}
		r.mb.cfg.OnDeliver(d)
	}
	return "Ok: delivered " + id, nil
}

func (mb *Mailbox) peerPK(c net.Conn) (cipher.PubKey, bool) {
	if mb.cfg.PeerPK == nil || c == nil {
		return cipher.PubKey{}, false
	}
	return mb.cfg.PeerPK(c)
}

// deliverLocal files body in the inbox under a trace header naming the
// PK the transport or the seal authenticated: the property a clearnet
// MTA needs SPF, DKIM and DMARC to approximate. A non-null relay is the
// visor that delivered a sealed envelope for peer.
func (mb *Mailbox) deliverLocal(peer, relay cipher.PubKey, body []byte) (string, error) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s: %s\r\n", PeerHeader, peer.Hex())
	from, with := peer, "ESMTP (skymail)"
	if !relay.Null() {
		fmt.Fprintf(&buf, "%s: %s\r\n", RelayHeader, relay.Hex())
		from, with = relay, "ESMTP (skymail sealed)"
	}
	fmt.Fprintf(&buf, "Received: from %s by %s with %s; %s\r\n",
		from.DNSLabel()+Suffixes[0], mb.cfg.PK.DNSLabel()+Suffixes[0], with, time.Now().Format(time.RFC1123Z))
	buf.Write(body)
	return mb.store(mb.inbox, buf.Bytes(), false)
}

func (mb *Mailbox) sealing() bool { return mb.cfg.Sealer != nil }

// relayOf is the RelayHeader the mailbox wrote, read from the line after
// its PeerHeader, so a header the sender put in the message never counts.
func relayOf(raw []byte) string {
	_, rest, ok := bytes.Cut(raw, []byte("\r\n"))
	if !ok || !bytes.HasPrefix(raw, []byte(PeerHeader+":")) {
		return ""
	}
	line, _, _ := bytes.Cut(rest, []byte("\r\n"))
	v, ok := bytes.CutPrefix(line, []byte(RelayHeader+": "))
	if !ok {
		return ""
	}
	var pk cipher.PubKey
	if pk.Set(string(v)) != nil {
		return ""
	}
	return pk.Hex()
}

// addressPK is the PK a skywire address names.
func addressPK(addr string) (cipher.PubKey, bool) {
	for _, sfx := range Suffixes {
		pk, _, ok, err := skymailbridge.ParseRecipient(addr, sfx, "a")
		if err == nil && ok {
			return pk, true
		}
	}
	return cipher.PubKey{}, false
}

func (mb *Mailbox) folder(name string) (*maildir, error) {
	switch {
	case name == "" || strings.EqualFold(name, FolderInbox):
		return mb.inbox, nil
	case strings.EqualFold(name, FolderSent):
		return mb.sent, nil
	case strings.EqualFold(name, FolderOutbox) && mb.out != nil:
		return mb.out.md, nil
	}
	return nil, fmt.Errorf("skymail: no folder %q", name)
}

// Summary is one message as a list shows it.
type Summary struct {
	ID      string    `json:"id"`
	Folder  string    `json:"folder"`
	From    string    `json:"from"`
	To      string    `json:"to"`
	Subject string    `json:"subject"`
	Date    time.Time `json:"date"`
	Seen    bool      `json:"seen"`
	Size    int64     `json:"size"`
	// PeerPK is the sender PK the transport authenticated (inbox only).
	PeerPK string `json:"peer_pk,omitempty"`
	// FromVerified is true when the From address names PeerPK itself.
	FromVerified bool `json:"from_verified"`
	// RelayPK is the visor that delivered a sealed envelope for PeerPK.
	RelayPK string `json:"relay_pk,omitempty"`
	// Queue is where an Outbox message stands.
	Queue *Queued `json:"queue,omitempty"`
}

// List returns a folder's messages, newest first.
func (mb *Mailbox) List(folder string) ([]Summary, error) {
	md, err := mb.folder(folder)
	if err != nil {
		return nil, err
	}
	es, err := md.entries()
	if err != nil {
		return nil, err
	}
	name := FolderInbox
	switch {
	case md == mb.sent:
		name = FolderSent
	case mb.isOutbox(md):
		name = FolderOutbox
	}
	maxAge := mb.Limits().MaxAge
	out := make([]Summary, 0, len(es))
	for _, e := range es {
		s := Summary{ID: e.id, Folder: name, Seen: strings.Contains(e.flags, "S"), Size: e.size}
		if raw, err := readHead(e.path); err == nil {
			if h, err := parseHeader(raw); err == nil {
				s.From, s.To, s.Subject = decodeHeader(h.Get("From")), decodeHeader(h.Get("To")), decodeHeader(h.Get("Subject"))
				if d, err := h.Date(); err == nil {
					s.Date = d
				}
				s.PeerPK = h.Get(PeerHeader)
				s.FromVerified = fromNamesPK(s.From, s.PeerPK)
				s.RelayPK = relayOf(raw)
			}
		}
		if mb.isOutbox(md) {
			s.Queue = mb.out.info(e.id, maxAge)
		}
		out = append(out, s)
	}
	return out, nil
}

// Raw returns a message exactly as stored.
func (mb *Mailbox) Raw(folder, id string) ([]byte, error) {
	md, err := mb.folder(folder)
	if err != nil {
		return nil, err
	}
	return md.read(id)
}

// MarkSeen flags a message as read.
func (mb *Mailbox) MarkSeen(folder, id string) error {
	md, err := mb.folder(folder)
	if err != nil {
		return err
	}
	return md.markSeen(id)
}

// Delete removes a message. In the Outbox that cancels its delivery.
func (mb *Mailbox) Delete(folder, id string) error {
	md, err := mb.folder(folder)
	if err != nil {
		return err
	}
	if mb.isOutbox(md) {
		return mb.out.drop(id)
	}
	return md.remove(id)
}

func (mb *Mailbox) isOutbox(md *maildir) bool { return mb.out != nil && md == mb.out.md }

func parseHeader(raw []byte) (mail.Header, error) {
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	return m.Header, nil
}

// fromNamesPK reports whether the From address is at peerHex's own
// .skynet/.dmsg domain, i.e. the sender cannot have forged it.
func fromNamesPK(from, peerHex string) bool {
	if peerHex == "" {
		return false
	}
	var peer cipher.PubKey
	if err := peer.Set(peerHex); err != nil {
		return false
	}
	a, err := mail.ParseAddress(from)
	if err != nil {
		return false
	}
	for _, sfx := range Suffixes {
		pk, _, ok, err := skymailbridge.ParseRecipient(a.Address, sfx, "a")
		if err == nil && ok {
			return pk == peer
		}
	}
	return false
}

// Attachment decodes attachment n of a message.
func (mb *Mailbox) Attachment(folder, id string, n int) (*AttachmentData, error) {
	raw, err := mb.Raw(folder, id)
	if err != nil {
		return nil, err
	}
	return ExtractAttachment(raw, n)
}
