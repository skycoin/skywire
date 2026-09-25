// Package skymail pkg/skymail/render.go c4-app-mail
package skymail

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strings"
)

// maxRenderedPart bounds how much of one decoded body part is kept.
const maxRenderedPart = 4 << 20

var wordDecoder = &mime.WordDecoder{
	// Only UTF-8 and ASCII-compatible charsets decode; anything else is
	// shown as its raw encoded-word rather than failing the whole list.
	CharsetReader: func(charset string, input io.Reader) (io.Reader, error) {
		switch strings.ToLower(charset) {
		case "utf-8", "us-ascii", "iso-8859-1", "latin1":
			return input, nil
		}
		return nil, io.EOF
	},
}

func decodeHeader(v string) string {
	if d, err := wordDecoder.DecodeHeader(v); err == nil {
		return d
	}
	return v
}

// Attachment names a non-text part of a message.
type Attachment struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
}

// Rendered is a message reduced to what a reader shows.
type Rendered struct {
	From        string       `json:"from"`
	To          string       `json:"to"`
	Cc          string       `json:"cc,omitempty"`
	Subject     string       `json:"subject"`
	Date        string       `json:"date"`
	MessageID   string       `json:"message_id,omitempty"`
	PeerPK      string       `json:"peer_pk,omitempty"`
	Verified    bool         `json:"from_verified"`
	Text        string       `json:"text"`
	FromHTML    bool         `json:"from_html,omitempty"` // Text was reduced from an HTML-only part
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Render parses raw into headers, a plain-text body, and the names of
// its attachments. It prefers text/plain; an HTML-only message is
// reduced to text, never rendered, so nothing in a message can run.
func Render(raw []byte) (*Rendered, error) {
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	h := m.Header
	r := &Rendered{
		From:      decodeHeader(h.Get("From")),
		To:        decodeHeader(h.Get("To")),
		Cc:        decodeHeader(h.Get("Cc")),
		Subject:   decodeHeader(h.Get("Subject")),
		Date:      h.Get("Date"),
		MessageID: h.Get("Message-Id"),
		PeerPK:    h.Get(PeerHeader),
	}
	r.Verified = fromNamesPK(r.From, r.PeerPK)

	var plain, html string
	walkPart(h.Get("Content-Type"), h.Get("Content-Transfer-Encoding"), h.Get("Content-Disposition"), m.Body, 0,
		func(ct, name string, body []byte) {
			switch {
			case name == "" && ct == "text/plain" && plain == "":
				plain = string(body)
			case name == "" && ct == "text/html" && html == "":
				html = string(body)
			default:
				r.Attachments = append(r.Attachments, Attachment{Name: name, ContentType: ct, Size: len(body)})
			}
		})
	switch {
	case plain != "":
		r.Text = plain
	case html != "":
		r.Text, r.FromHTML = htmlToText(html), true
	}
	return r, nil
}

// walkPart visits every leaf part, decoding its transfer encoding.
func walkPart(ctHeader, cte, disp string, body io.Reader, depth int, visit func(ct, name string, body []byte)) {
	if ctHeader == "" {
		ctHeader = "text/plain"
	}
	ct, params, err := mime.ParseMediaType(ctHeader)
	if err != nil {
		ct, params = "text/plain", nil
	}
	if strings.HasPrefix(ct, "multipart/") && depth < 8 {
		mr := multipart.NewReader(body, params["boundary"])
		for {
			p, err := mr.NextRawPart()
			if err != nil {
				return
			}
			walkPart(p.Header.Get("Content-Type"), p.Header.Get("Content-Transfer-Encoding"),
				p.Header.Get("Content-Disposition"), p, depth+1, visit)
		}
	}
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "base64":
		body = base64.NewDecoder(base64.StdEncoding, newlineStripper{body})
	case "quoted-printable":
		body = quotedprintable.NewReader(body)
	}
	data, _ := io.ReadAll(io.LimitReader(body, maxRenderedPart)) //nolint:errcheck // a truncated part still renders
	name := params["name"]
	if _, dp, err := mime.ParseMediaType(disp); err == nil && dp["filename"] != "" {
		name = dp["filename"]
	}
	if strings.HasPrefix(strings.ToLower(disp), "attachment") && name == "" {
		name = "attachment"
	}
	visit(ct, decodeHeader(name), data)
}

// newlineStripper drops CR and LF so base64 split across lines decodes.
type newlineStripper struct{ r io.Reader }

func (s newlineStripper) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	j := 0
	for i := 0; i < n; i++ {
		if p[i] != '\r' && p[i] != '\n' {
			p[j] = p[i]
			j++
		}
	}
	return j, err
}

var (
	reHTMLDrop  = regexp.MustCompile(`(?is)<(script|style|head)[^>]*>.*?</(script|style|head)>`)
	reHTMLBreak = regexp.MustCompile(`(?i)<\s*(br|/p|/div|/tr|/li|/h[1-6])\s*/?>`)
	reHTMLTag   = regexp.MustCompile(`(?s)<[^>]*>`)
	reBlank     = regexp.MustCompile(`\n{3,}`)
)

func htmlToText(s string) string {
	s = reHTMLDrop.ReplaceAllString(s, "")
	s = reHTMLBreak.ReplaceAllString(s, "\n")
	s = reHTMLTag.ReplaceAllString(s, "")
	s = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'").Replace(s)
	return strings.TrimSpace(reBlank.ReplaceAllString(s, "\n\n"))
}
