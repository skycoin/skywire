// Package deskhost pkg/wasmhv/deskhost/mail_helpers.go c4-wasm-desk
//
// The mail app calls the tab visor's mail API (visorapi.Mail) over its
// RPC port, the same methods `skywire cli mail` calls, so the window and
// the CLI cannot drift. These are the parts of it that need no DOM.
package deskhost

import (
	"net/mail"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// splitAddrs reads a To/Cc field: addresses separated by commas,
// semicolons or whitespace.
func splitAddrs(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
	})
}

// replyTo prefills a reply to m, sent from whichever of this mailbox's
// addresses it was sent to (own is any one of them).
func replyTo(m *skymail.Rendered, own string) skymail.Outgoing {
	subj := m.Subject
	if !strings.HasPrefix(strings.ToLower(subj), "re:") {
		subj = "Re: " + subj
	}
	var quoted strings.Builder
	quoted.WriteString("\n\n")
	quoted.WriteString(m.Date + ", " + m.From + " wrote:\n")
	for _, line := range strings.Split(strings.TrimRight(strings.ReplaceAll(m.Text, "\r\n", "\n"), "\n"), "\n") {
		quoted.WriteString("> " + line + "\n")
	}
	to := m.From
	if i, j := strings.LastIndex(to, "<"), strings.LastIndex(to, ">"); i >= 0 && j > i {
		to = to[i+1 : j]
	}
	return skymail.Outgoing{
		From: addressedTo(m, own), To: []string{to}, Subject: subj, Body: quoted.String(), InReplyTo: m.MessageID,
	}
}

// addressedTo is the first To or Cc address at own's PK label, or "".
func addressedTo(m *skymail.Rendered, own string) string {
	label := pkLabel(own)
	if label == "" {
		return ""
	}
	for _, field := range []string{m.To, m.Cc} {
		list, err := mail.ParseAddressList(field)
		if err != nil {
			continue
		}
		for _, a := range list {
			if pkLabel(a.Address) == label {
				return a.Address
			}
		}
	}
	return ""
}

// pkLabel is the PK label of a skywire address: the domain label just
// before .skynet or .dmsg, lowercased.
func pkLabel(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return ""
	}
	labels := strings.Split(strings.ToLower(addr[at+1:]), ".")
	if len(labels) < 2 {
		return ""
	}
	return labels[len(labels)-2]
}

// withPK and withoutPK edit a whitelist.
func withPK(cur []cipher.PubKey, pk cipher.PubKey) []cipher.PubKey {
	for _, p := range cur {
		if p == pk {
			return cur
		}
	}
	return append(append([]cipher.PubKey{}, cur...), pk)
}

func withoutPK(cur []cipher.PubKey, pk cipher.PubKey) []cipher.PubKey {
	out := []cipher.PubKey{}
	for _, p := range cur {
		if p != pk {
			out = append(out, p)
		}
	}
	return out
}

// settingsUpdate reads the settings form: every field as `mail settings`
// takes it ("16MiB", "7d", "default", "none").
func settingsUpdate(enable bool, maxMessage, maxTotal, maxAge string) (visorapi.MailSettingsUpdate, error) {
	u := visorapi.MailSettingsUpdate{Enable: &enable}
	msg, err := skymail.ParseSize(maxMessage)
	if err != nil {
		return u, err
	}
	total, err := skymail.ParseSize(maxTotal)
	if err != nil {
		return u, err
	}
	age, err := skymail.ParseAge(maxAge)
	if err != nil {
		return u, err
	}
	u.MaxMessageSize, u.MaxTotalSize, u.MaxAge = &msg, &total, &age
	return u, nil
}

// delivered counts the recipients a send reached.
func delivered(res *skymail.SendResult) (ok, via []string, failed []string) {
	for _, r := range res.Recipients {
		if r.Err == "" {
			ok, via = append(ok, r.Rcpt), append(via, r.Via)
		} else {
			failed = append(failed, r.Rcpt+": "+r.Err)
		}
	}
	return ok, via, failed
}
