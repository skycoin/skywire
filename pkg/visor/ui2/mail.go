// Package ui2 pkg/visor/ui2/mail.go c3-vis-core
package ui2

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// Mail folders, as the visor names them.
const (
	FolderInbox  = "INBOX"
	FolderSent   = "Sent"
	FolderOutbox = "Outbox"
)

// Mail is what the mail tab shows. View is "list", "message", "compose" or
// "settings".
type Mail struct {
	PK        string
	Status    *visorapi.MailStatus
	StatusErr string
	Folder    string
	View      string
	List      []skymail.Summary
	Msg       *skymail.Rendered
	MsgID     string
	Queue     *skymail.Queued
	Draft     Draft
	Report    []skymail.RcptResult
	Err       string
}

// Draft is the compose form.
type Draft struct {
	To, Cc, Subject, Body, InReplyTo string
}

// Folder is a known folder name, INBOX for anything else.
func Folder(s string) string {
	switch s {
	case FolderSent, FolderOutbox:
		return s
	}
	return FolderInbox
}

// MailURL is a mail tab URL with query pairs.
func MailURL(pk, rest string, kv ...string) string {
	u := "visors/" + pk + "/mail" + rest
	q := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			q.Set(kv[i], kv[i+1])
		}
	}
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// MailAPI is a JSON mail route for a message, such as its raw download.
func MailAPI(pk, folder, id, rest string) string {
	return "api/visors/" + pk + "/mail/" + url.PathEscape(folder) + "/" + url.PathEscape(id) + rest
}

// Reply fills a draft answering m, as the Angular tab does.
func Reply(folder string, m *skymail.Rendered) Draft {
	d := Draft{To: m.From, InReplyTo: m.MessageID}
	if folder == FolderSent {
		d.To = m.To
	}
	d.Subject = m.Subject
	if !regexp.MustCompile(`(?i)^re:`).MatchString(d.Subject) {
		d.Subject = "Re: " + d.Subject
	}
	var b strings.Builder
	b.WriteString("\n\n")
	for i, l := range strings.Split(strings.TrimRight(m.Text, " \t\r\n"), "\n") {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("> " + l)
	}
	d.Body = b.String()
	return d
}

// Addrs splits an address field on commas, semicolons and spaces.
func Addrs(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' })
}

// ParsePKList reads one key per line or comma separated.
func ParsePKList(s string) ([]cipher.PubKey, error) {
	var out []cipher.PubKey
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t' }) {
		var pk cipher.PubKey
		if err := pk.Set(f); err != nil {
			return nil, fmt.Errorf("key %q: %w", f, err)
		}
		out = append(out, pk)
	}
	return out, nil
}

// PKLines is keys one per line.
func PKLines(pks []cipher.PubKey) string {
	s := make([]string, len(pks))
	for i, pk := range pks {
		s[i] = pk.Hex()
	}
	return strings.Join(s, "\n")
}

// ListText is a helper or relay list for its textarea: empty for the
// default, "none" for an empty list.
func ListText(pks []cipher.PubKey, def bool) string {
	if def {
		return ""
	}
	if len(pks) == 0 {
		return "none"
	}
	return PKLines(pks)
}

// Size prints a byte count.
func Size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// When prints a time in local time, or "" for the zero time.
func When(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}
