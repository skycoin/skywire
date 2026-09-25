// Package deskhost pkg/wasmhv/deskhost/mailcmd.go c4-wasm-desk
//
// The mail app drives the tab's visor through `skywire cli mail`, exactly
// as a user at the terminal would, so the window and the CLI cannot drift.
// These builders turn what the window holds into one shell command line;
// every value is single-quoted, so nothing a message contains is ever
// parsed as shell.
package deskhost

import (
	"encoding/json"
	"errors"
	"strings"

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

// replyTo prefills a reply to m.
func replyTo(m *skymail.Rendered) skymail.Outgoing {
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
	return skymail.Outgoing{To: []string{to}, Subject: subj, Body: quoted.String(), InReplyTo: m.MessageID}
}
