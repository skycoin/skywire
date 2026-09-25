// Package skymail pkg/skymail/send.go c4-app-mail
package skymail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skymailbridge"
)

// Outgoing is a plain-text message to send.
type Outgoing struct {
	// From is a local part ("me") or a full address of this mailbox.
	// Empty means "mail".
	From      string   `json:"from,omitempty"`
	To        []string `json:"to"`
	Cc        []string `json:"cc,omitempty"`
	Subject   string   `json:"subject"`
	Body      string   `json:"body"`
	InReplyTo string   `json:"in_reply_to,omitempty"`
}

// RcptResult is the outcome for one recipient. Delivery is immediate:
// a recipient whose visor cannot be reached now gets an error, not a
// queue entry.
type RcptResult struct {
	Rcpt string `json:"rcpt"`
	Err  string `json:"error,omitempty"`
}

// SendResult is what Send reports.
type SendResult struct {
	ID         string       `json:"id,omitempty"` // the Sent copy, when anything was delivered
	MessageID  string       `json:"message_id"`
	Recipients []RcptResult `json:"recipients"`
}

// peerGroup is the recipients one SMTP transaction carries: same PK,
// same network.
type peerGroup struct {
	suffix string
	pk     cipher.PubKey
	rcpts  []skymailbridge.Recipient
}

// Send delivers out to every recipient's visor now, over the network
// its address names, and files a copy in Sent if any delivery worked.
// Recipients at this mailbox's own PK are filed directly.
func (mb *Mailbox) Send(ctx context.Context, out Outgoing) (*SendResult, error) {
	all := append(append([]string{}, out.To...), out.Cc...)
	if len(all) == 0 {
		return nil, errors.New("skymail: no recipients")
	}
	// Header values go into the message verbatim; a line break in one
	// would let it write headers of its own.
	for _, v := range append([]string{out.From, out.InReplyTo}, all...) {
		if strings.ContainsAny(v, "\r\n") {
			return nil, errors.New("skymail: line break in an address or reference")
		}
	}
	var (
		groups []*peerGroup
		res    = &SendResult{}
		bad    []RcptResult
	)
	for _, addr := range all {
		addr = strings.TrimSpace(addr)
		g, r, err := parseRcpt(addr)
		if err != nil {
			bad = append(bad, RcptResult{Rcpt: addr, Err: err.Error()})
			continue
		}
		var found *peerGroup
		for _, pg := range groups {
			if pg.pk == g.pk && pg.suffix == g.suffix {
				found = pg
				break
			}
		}
		if found == nil {
			found = g
			groups = append(groups, g)
		}
		found.rcpts = append(found.rcpts, r)
	}
	if len(groups) == 0 {
		res.Recipients = bad
		return res, errors.New("skymail: no deliverable recipients")
	}

	from, err := mb.fromAddress(out.From, groups[0].suffix)
	if err != nil {
		return nil, err
	}
	msgID, err := newID()
	if err != nil {
		return nil, err
	}
	res.MessageID = "<" + msgID + "@" + mb.cfg.PK.DNSLabel() + groups[0].suffix + ">"
	msg := compose(from, out, res.MessageID)

	results := make([][]RcptResult, len(groups))
	var wg sync.WaitGroup
	for i, g := range groups {
		wg.Add(1)
		go func(i int, g *peerGroup) {
			defer wg.Done()
			err := mb.deliverGroup(ctx, from, g, msg)
			if err != nil {
				mb.log.WithError(err).WithField("peer", g.pk.Hex()).WithField("via", g.suffix).
					Warn("skymail: not delivered")
			}
			for _, r := range g.rcpts {
				rr := RcptResult{Rcpt: r.Original}
				if err != nil {
					rr.Err = err.Error()
				}
				results[i] = append(results[i], rr)
			}
		}(i, g)
	}
	wg.Wait()

	delivered := 0
	for _, rs := range results {
		for _, r := range rs {
			if r.Err == "" {
				delivered++
			}
		}
		res.Recipients = append(res.Recipients, rs...)
	}
	res.Recipients = append(res.Recipients, bad...)
	if delivered == 0 {
		return res, errors.New("skymail: not delivered to any recipient")
	}
	if id, err := mb.sent.put(msg, true); err != nil {
		mb.log.WithError(err).Warn("skymail: keep Sent copy")
	} else {
		res.ID = id
	}
	return res, nil
}

func (mb *Mailbox) deliverGroup(ctx context.Context, from string, g *peerGroup, msg []byte) error {
	if g.pk == mb.cfg.PK {
		_, err := mb.deliverLocal(mb.cfg.PK, msg)
		return err
	}
	dialer := mb.cfg.Dialers[g.suffix]
	if dialer == nil {
		return fmt.Errorf("no route to %s addresses from this visor", g.suffix)
	}
	cfg := skymailbridge.Config{
		Suffix:     g.suffix,
		Mode:       "b",
		HeloName:   mb.cfg.PK.DNSLabel() + g.suffix,
		RemotePort: 25,
	}
	return skymailbridge.Relay(ctx, dialer, cfg, from, g.rcpts, msg, mb.log)
}

// parseRcpt accepts only skywire addresses: this mailbox sends nowhere
// else. Mode "b" strips a host part the way the Postfix bridge does, so
// user@<vhost>.<pk>.skynet reaches the right mailbox behind Postfix.
func parseRcpt(addr string) (*peerGroup, skymailbridge.Recipient, error) {
	if a, err := mail.ParseAddress(addr); err == nil {
		addr = a.Address
	}
	for _, sfx := range Suffixes {
		pk, fwd, ok, err := skymailbridge.ParseRecipient(addr, sfx, "b")
		if err != nil {
			return nil, skymailbridge.Recipient{}, err
		}
		if ok {
			return &peerGroup{suffix: sfx, pk: pk},
				skymailbridge.Recipient{Original: addr, Forward: fwd, PeerPK: pk}, nil
		}
	}
	return nil, skymailbridge.Recipient{}, fmt.Errorf("not a skywire address (want user@<pk>%s)", Suffixes[0])
}

func (mb *Mailbox) fromAddress(from, suffix string) (string, error) {
	if !strings.Contains(from, "@") {
		return mb.Address(from, suffix), nil
	}
	local, err := mb.isLocal(from)
	if err != nil {
		return "", err
	}
	if !local {
		return "", fmt.Errorf("skymail: %s is not an address of this visor", from)
	}
	return from, nil
}

// compose builds an RFC 5322 message. The body goes quoted-printable
// so any line length and any UTF-8 survive every hop unchanged.
func compose(from string, out Outgoing, msgID string) []byte {
	var b bytes.Buffer
	hdr := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	hdr("Date", time.Now().Format(time.RFC1123Z))
	hdr("From", from)
	hdr("To", strings.Join(out.To, ", "))
	if len(out.Cc) > 0 {
		hdr("Cc", strings.Join(out.Cc, ", "))
	}
	hdr("Subject", mime.QEncoding.Encode("utf-8", out.Subject))
	hdr("Message-ID", msgID)
	if out.InReplyTo != "" {
		hdr("In-Reply-To", out.InReplyTo)
		hdr("References", out.InReplyTo)
	}
	hdr("MIME-Version", "1.0")
	hdr("Content-Type", "text/plain; charset=utf-8")
	hdr("Content-Transfer-Encoding", "quoted-printable")
	b.WriteString("\r\n")
	body := strings.ReplaceAll(strings.ReplaceAll(out.Body, "\r\n", "\n"), "\n", "\r\n")
	qp := quotedprintable.NewWriter(&b)
	_, _ = qp.Write([]byte(body)) //nolint:errcheck // bytes.Buffer cannot fail
	_ = qp.Close()                //nolint:errcheck
	b.WriteString("\r\n")
	return b.Bytes()
}
