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
	Log    logrus.FieldLogger
}

// Mailbox is one PK's mail: a Maildir, an SMTP receiver policy, and a
// sender. Safe for concurrent use.
type Mailbox struct {
	cfg   Config
	inbox *maildir
	sent  *maildir
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
	log := cfg.Log
	if log == nil {
		log = logrus.NewEntry(logrus.New())
	}
	mb := &Mailbox{cfg: cfg, inbox: inbox, sent: sent, log: log}
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

func (r receiver) Rcpt(c net.Conn, _, rcpt string, _ []string) error {
	peer, ok := r.mb.peerPK(c)
	if !ok || !r.mb.allowed(peer) {
		return &skymailbridge.Reply{Code: 550, Enhanced: "5.7.1", Text: "sender PK not whitelisted by this mailbox"}
	}
	local, err := r.mb.isLocal(rcpt)
	if err != nil {
		return &skymailbridge.Reply{Code: 550, Enhanced: "5.1.3", Text: err.Error()}
	}
	if !local {
		// A mailbox, never a relay.
		return &skymailbridge.Reply{Code: 550, Enhanced: "5.7.1", Text: rcpt + " is not a mailbox of this visor"}
	}
	if r.mb.full() {
		return &skymailbridge.Reply{Code: 452, Enhanced: "4.2.2", Text: "mailbox full"}
	}
	return nil
}

func (r receiver) Deliver(_ context.Context, env skymailbridge.Envelope) (string, error) {
	peer, _ := r.mb.peerPK(env.Conn)
	id, err := r.mb.deliverLocal(peer, env.Body)
	if errors.Is(err, ErrMailboxFull) {
		return "", &skymailbridge.Reply{Code: 452, Enhanced: "4.2.2", Text: "mailbox full"}
	}
	if err != nil {
		r.mb.log.WithError(err).Warn("skymail: store")
		return "", &skymailbridge.Reply{Code: 451, Enhanced: "4.3.0", Text: "mailbox store failed"}
	}
	r.mb.log.WithField("from_pk", peer.Hex()).WithField("id", id).WithField("bytes", len(env.Body)).
		Info("skymail: delivered")
	return "Ok: delivered " + id, nil
}

func (mb *Mailbox) peerPK(c net.Conn) (cipher.PubKey, bool) {
	if mb.cfg.PeerPK == nil || c == nil {
		return cipher.PubKey{}, false
	}
	return mb.cfg.PeerPK(c)
}

// deliverLocal files body in the inbox under a trace header naming the
// PK the transport authenticated: the property a clearnet MTA needs
// SPF, DKIM and DMARC to approximate.
func (mb *Mailbox) deliverLocal(peer cipher.PubKey, body []byte) (string, error) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s: %s\r\n", PeerHeader, peer.Hex())
	fmt.Fprintf(&buf, "Received: from %s by %s with ESMTP (skymail); %s\r\n",
		peer.DNSLabel()+Suffixes[0], mb.cfg.PK.DNSLabel()+Suffixes[0], time.Now().Format(time.RFC1123Z))
	buf.Write(body)
	return mb.store(mb.inbox, buf.Bytes(), false)
}

func (mb *Mailbox) folder(name string) (*maildir, error) {
	switch {
	case name == "" || strings.EqualFold(name, FolderInbox):
		return mb.inbox, nil
	case strings.EqualFold(name, FolderSent):
		return mb.sent, nil
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
	if md == mb.sent {
		name = FolderSent
	}
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
			}
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

// Delete removes a message.
func (mb *Mailbox) Delete(folder, id string) error {
	md, err := mb.folder(folder)
	if err != nil {
		return err
	}
	return md.remove(id)
}

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
