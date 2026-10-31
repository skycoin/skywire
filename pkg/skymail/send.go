// Package skymail pkg/skymail/send.go c4-app-mail
package skymail

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/textproto"
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
	// Attachments go after the text as a multipart/mixed message.
	Attachments []OutgoingAttachment `json:"attachments,omitempty"`
}

// OutgoingAttachment is one file to send.
type OutgoingAttachment struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type,omitempty"` // default application/octet-stream
	Data        []byte `json:"data"`
}

// RcptResult is the outcome for one recipient. Err says why it was not
// delivered. With Queued set that is only for now: the Outbox retries.
type RcptResult struct {
	Rcpt   string `json:"rcpt"`
	Err    string `json:"error,omitempty"`
	Queued bool   `json:"queued,omitempty"`
	// Via is the network that carried it: "skynet", "dmsg", or "local"
	// for this mailbox's own PK.
	Via string `json:"via,omitempty"`
}

// SendResult is what Send reports.
type SendResult struct {
	ID         string       `json:"id,omitempty"`        // the Sent copy, when anything was delivered
	OutboxID   string       `json:"outbox_id,omitempty"` // the Outbox copy, when anything was queued
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
// Recipients at this mailbox's own PK are filed directly. A recipient
// that cannot take it yet is queued in the Outbox, and Send succeeds
// when every recipient was either delivered or queued.
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
	res := &SendResult{}
	groups, bad := groupRcpts(all)
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

	var (
		delivered int
		pending   []string
		lastErr   string
	)
	for _, gr := range mb.deliverGroups(ctx, from, groups, msg) {
		queue := gr.err != nil && mb.retryable(gr.g, gr.err)
		for _, r := range gr.g.rcpts {
			rr := RcptResult{Rcpt: r.Original, Via: gr.via}
			switch {
			case gr.err == nil:
				delivered++
			case queue:
				rr.Err, rr.Via, rr.Queued = gr.err.Error(), "", true
				pending = append(pending, r.Original)
				lastErr = gr.err.Error()
			default:
				rr.Err, rr.Via = gr.err.Error(), ""
			}
			res.Recipients = append(res.Recipients, rr)
		}
	}
	res.Recipients = append(res.Recipients, bad...)
	if delivered > 0 {
		if id, err := mb.store(mb.sent, msg, true); err != nil {
			mb.log.WithError(err).Warn("skymail: keep Sent copy")
		} else {
			res.ID = id
		}
	}
	if len(pending) > 0 {
		id, err := mb.enqueue(msg, queueRecord{
			From: from, Pending: pending, SentCopy: delivered > 0, Attempts: 1, LastError: lastErr,
		})
		if err != nil {
			mb.log.WithError(err).Warn("skymail: queue in Outbox")
			for i := range res.Recipients {
				if res.Recipients[i].Queued {
					res.Recipients[i].Queued = false
					res.Recipients[i].Err += " (not queued: " + err.Error() + ")"
				}
			}
			pending = nil
		} else {
			res.OutboxID = id
		}
	}
	if delivered == 0 && len(pending) == 0 {
		return res, errors.New("skymail: not delivered to any recipient")
	}
	return res, nil
}

// groupRcpts sorts addresses into one SMTP transaction per PK and
// network, and reports those that are not skywire addresses.
func groupRcpts(addrs []string) ([]*peerGroup, []RcptResult) {
	var (
		groups []*peerGroup
		bad    []RcptResult
	)
	for _, addr := range addrs {
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
	return groups, bad
}

// groupResult is how one transaction went.
type groupResult struct {
	g   *peerGroup
	via string
	err error
}

// deliverGroups runs every transaction at once.
func (mb *Mailbox) deliverGroups(ctx context.Context, from string, groups []*peerGroup, msg []byte) []groupResult {
	results := make([]groupResult, len(groups))
	var wg sync.WaitGroup
	for i, g := range groups {
		wg.Go(func() {
			via, err := mb.deliverGroup(ctx, from, g, msg)
			if err != nil {
				mb.log.WithError(err).WithField("peer", g.pk.Hex()).WithField("via", g.suffix).
					Warn("skymail: not delivered")
			}
			results[i] = groupResult{g: g, via: via, err: err}
		})
	}
	wg.Wait()
	return results
}

// deliverGroup delivers one SMTP transaction and names the network that
// carried it.
func (mb *Mailbox) deliverGroup(ctx context.Context, from string, g *peerGroup, msg []byte) (string, error) {
	if g.pk == mb.cfg.PK {
		_, err := mb.deliverLocal(mb.cfg.PK, msg)
		return "local", err
	}
	dialer := mb.cfg.Dialers[g.suffix]
	if dialer == nil {
		return "", fmt.Errorf("no route to %s addresses from this visor", g.suffix)
	}
	cfg := skymailbridge.Config{
		Suffix:     g.suffix,
		Mode:       "b",
		HeloName:   mb.cfg.PK.DNSLabel() + g.suffix,
		RemotePort: 25,
	}
	rd := &recordingDialer{d: dialer, life: ctx}
	err := skymailbridge.Relay(ctx, rd, cfg, from, g.rcpts, msg, mb.log)
	if rd.stop != nil {
		rd.stop()
	}
	return rd.network, err
}

// recordingDialer notes the network of the connection it hands out: a
// .skynet address may have fallen back to dmsg, and the sender wants to
// know. The connection closes when life ends, since Relay bounds only
// the dial and a peer that stalls mid-session would hold it forever.
type recordingDialer struct {
	d       skymailbridge.Dialer
	life    context.Context
	network string
	stop    func() bool
}

func (r *recordingDialer) Dial(ctx context.Context, peer cipher.PubKey, port uint16) (net.Conn, error) {
	c, err := r.d.Dial(ctx, peer, port)
	if err == nil {
		r.network = c.RemoteAddr().Network()
		r.stop = context.AfterFunc(r.life, func() { _ = c.Close() }) //nolint:errcheck
	}
	return c, err
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

// compose builds an RFC 5322 message. Text goes quoted-printable so any
// line length and any UTF-8 survive every hop unchanged; attachments go
// base64 in a multipart/mixed after it.
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
	if len(out.Attachments) == 0 {
		hdr("Content-Type", textContentType)
		hdr("Content-Transfer-Encoding", "quoted-printable")
		b.WriteString("\r\n")
		writeQP(&b, out.Body)
		return b.Bytes()
	}
	mw := multipart.NewWriter(&b)
	hdr("Content-Type", mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": mw.Boundary()}))
	b.WriteString("\r\n")
	tw, _ := mw.CreatePart(textproto.MIMEHeader{ //nolint:errcheck // bytes.Buffer cannot fail
		"Content-Type":              {textContentType},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	writeQP(tw, out.Body)
	for _, a := range out.Attachments {
		ct := a.ContentType
		if _, _, err := mime.ParseMediaType(ct); ct == "" || err != nil {
			ct = "application/octet-stream"
		}
		name := a.Name
		if name == "" {
			name = "attachment"
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Type", ct)
		h.Set("Content-Transfer-Encoding", "base64")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		pw, _ := mw.CreatePart(h) //nolint:errcheck // bytes.Buffer cannot fail
		enc := base64.StdEncoding.EncodeToString(a.Data)
		for len(enc) > 76 {
			_, _ = pw.Write([]byte(enc[:76] + "\r\n")) //nolint:errcheck
			enc = enc[76:]
		}
		_, _ = pw.Write([]byte(enc + "\r\n")) //nolint:errcheck
	}
	_ = mw.Close() //nolint:errcheck
	return b.Bytes()
}

const textContentType = "text/plain; charset=utf-8"

// writeQP writes body quoted-printable with CRLF line breaks.
func writeQP(w io.Writer, body string) {
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	qp := quotedprintable.NewWriter(w)
	_, _ = qp.Write([]byte(body)) //nolint:errcheck
	_ = qp.Close()                //nolint:errcheck
	fmt.Fprint(w, "\r\n")         //nolint:errcheck
}
