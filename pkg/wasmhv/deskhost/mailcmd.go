// Package deskhost pkg/wasmhv/deskhost/mailcmd.go c4-wasm-desk
//
// The mail app drives the tab's visor through `skywire cli mail`, exactly
// as a user at the terminal would, so the window and the CLI cannot drift.
// These builders turn what the window holds into one shell command line;
// every value is single-quoted, so nothing a message contains is ever
// parsed as shell.
package deskhost

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/skymail"
)

// shQuote makes s one literal POSIX shell word.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func mailFolderFlag(folder string) string {
	if folder == skymail.FolderSent {
		return " --sent"
	}
	return ""
}

func mailListCmd(folder string) string {
	if folder == skymail.FolderSent {
		return "skywire cli mail sent --json"
	}
	return "skywire cli mail inbox --json"
}

func mailReadCmd(folder, id string) string {
	return "skywire cli mail read" + mailFolderFlag(folder) + " --json -- " + shQuote(id)
}

func mailRmCmd(folder, id string) string {
	return "skywire cli mail rm" + mailFolderFlag(folder) + " --json -- " + shQuote(id)
}

const mailStatusCmd = "skywire cli mail --json"

func mailWhitelistCmd(op string, pks []string) string {
	cmd := "skywire cli mail whitelist --json"
	if op == "" {
		return cmd
	}
	cmd += " -- " + shQuote(op)
	for _, pk := range pks {
		cmd += " " + shQuote(pk)
	}
	return cmd
}

// mailSendCmd puts every flag before "--" so a recipient that begins
// with "-" is still a recipient.
func mailSendCmd(out skymail.Outgoing) string {
	var b strings.Builder
	b.WriteString("skywire cli mail send --json")
	b.WriteString(" -s " + shQuote(out.Subject))
	b.WriteString(" -m " + shQuote(out.Body))
	if out.From != "" {
		b.WriteString(" --from " + shQuote(out.From))
	}
	for _, cc := range out.Cc {
		b.WriteString(" --cc " + shQuote(cc))
	}
	if out.InReplyTo != "" {
		b.WriteString(" --in-reply-to " + shQuote(out.InReplyTo))
	}
	for _, a := range out.Attachments {
		b.WriteString(" --attach-base64 " + shQuote(a.Name+"="+base64.StdEncoding.EncodeToString(a.Data)))
	}
	b.WriteString(" --")
	for _, to := range out.To {
		b.WriteString(" " + shQuote(to))
	}
	return b.String()
}

// splitAddrs reads a To/Cc field: addresses separated by commas,
// semicolons or whitespace.
func splitAddrs(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
	})
}

// cliResult decodes a `--json` command's output into v. A failed command
// prints {"error": "..."}, which comes back as the error.
func cliResult(out string, runErr error, v any) error {
	out = strings.TrimSpace(out)
	var fail struct {
		Error string `json:"error"`
	}
	if runErr != nil || strings.HasPrefix(out, `{"error"`) || strings.HasPrefix(out, "{\n  \"error\"") {
		if json.Unmarshal([]byte(out), &fail) == nil && fail.Error != "" {
			return errors.New(fail.Error)
		}
		if runErr != nil {
			if out != "" {
				return errors.New(out)
			}
			return runErr
		}
	}
	if v == nil {
		return nil
	}
	return json.Unmarshal([]byte(out), v)
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

func mailAttachmentCmd(folder, id string, n int) string {
	return "skywire cli mail attachment" + mailFolderFlag(folder) + " --json -- " + shQuote(id) + " " + strconv.Itoa(n)
}

func mailSettingsCmd(kvs []string) string {
	cmd := "skywire cli mail settings --json"
	if len(kvs) == 0 {
		return cmd
	}
	cmd += " --"
	for _, kv := range kvs {
		cmd += " " + shQuote(kv)
	}
	return cmd
}

// fmtSize and fmtAge write limits the way `mail settings` reads them.
func fmtSize(n int64) string {
	switch {
	case n < 0:
		return "none"
	case n >= 1<<20 && n%(1<<20) == 0:
		return strconv.FormatInt(n>>20, 10) + "MiB"
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + "MiB"
	case n >= 1<<10:
		return strconv.FormatFloat(float64(n)/(1<<10), 'f', 0, 64) + "KiB"
	}
	return strconv.FormatInt(n, 10) + "B"
}

func fmtAge(d time.Duration) string {
	switch {
	case d < 0:
		return "none"
	case d%(24*time.Hour) == 0:
		return strconv.FormatInt(int64(d/(24*time.Hour)), 10) + "d"
	}
	return d.String()
}
