package deskhost

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/shell"

	"github.com/skycoin/skywire/pkg/skymail"
)

func words(t *testing.T, cmd string) []string {
	t.Helper()
	w, err := shell.Fields(cmd, func(string) string { return "EXPANDED" })
	require.NoError(t, err)
	return w
}

// TestMailSendCmdKeepsEveryValueLiteral: whatever a message holds,
// the shell hands it to the CLI as one argument, unexpanded.
func TestMailSendCmdKeepsEveryValueLiteral(t *testing.T) {
	hostile := "it's $HOME `id` $(rm -rf /) \"q\" \\ ; && | > x\nline two\n'"
	out := skymail.Outgoing{
		From: "me", To: []string{"-bob@x.skynet", "carol@y.dmsg"}, Cc: []string{"dave@z.skynet"},
		Subject: hostile, Body: hostile, InReplyTo: "<id@x>",
	}
	got := words(t, mailSendCmd(out))
	require.Equal(t, []string{
		"skywire", "cli", "mail", "send", "--json",
		"-s", hostile, "-m", hostile,
		"--from", "me", "--cc", "dave@z.skynet", "--in-reply-to", "<id@x>",
		"--", "-bob@x.skynet", "carol@y.dmsg",
	}, got)
}

func TestMailReadAndRmCmds(t *testing.T) {
	require.Equal(t, []string{"skywire", "cli", "mail", "read", "--json", "--", "1.M2.skymail"},
		words(t, mailReadCmd(skymail.FolderInbox, "1.M2.skymail")))
	require.Equal(t, []string{"skywire", "cli", "mail", "rm", "--sent", "--json", "--", "a'b"},
		words(t, mailRmCmd(skymail.FolderSent, "a'b")))
	require.Equal(t, []string{"skywire", "cli", "mail", "whitelist", "--json", "--", "add", "02ab", "03cd"},
		words(t, mailWhitelistCmd("add", []string{"02ab", "03cd"})))
}

func TestCliResult(t *testing.T) {
	var v []skymail.Summary
	require.NoError(t, cliResult(`[{"id":"a","subject":"s"}]`, nil, &v))
	require.Equal(t, "s", v[0].Subject)

	err := cliResult("{\n  \"error\": \"mailbox is not running on this visor\"\n}\n", errors.New("exit 1"), &v)
	require.EqualError(t, err, "mailbox is not running on this visor")

	require.EqualError(t, cliResult("boom", errors.New("exit 1"), nil), "boom")
	require.EqualError(t, cliResult("", errors.New("exit 1"), nil), "exit 1")
}

func TestReplyTo(t *testing.T) {
	r := replyTo(&skymail.Rendered{
		From: "Bob <bob@x.skynet>", Subject: "hi", Date: "Mon", MessageID: "<m@x>", Text: "one\r\ntwo\r\n",
		To: "carol@elsewhere.skynet, Alice <alice@host.MINE.skynet>",
	}, "mail@mine.skynet")
	require.Equal(t, []string{"bob@x.skynet"}, r.To)
	require.Equal(t, "Re: hi", r.Subject)
	require.Equal(t, "<m@x>", r.InReplyTo)
	require.Equal(t, "alice@host.MINE.skynet", r.From, "reply from the address it was sent to")
	require.Equal(t, "\n\nMon, Bob <bob@x.skynet> wrote:\n> one\n> two\n", r.Body)
	require.Equal(t, "RE: hi", replyTo(&skymail.Rendered{Subject: "RE: hi"}, "").Subject)
}

func TestSplitAddrs(t *testing.T) {
	require.Equal(t, []string{"a@x", "b@y", "c@z"}, splitAddrs(" a@x, b@y;\nc@z "))
	require.Empty(t, splitAddrs("  "))
}

func TestMailListAndStatusCmds(t *testing.T) {
	require.Equal(t, []string{"skywire", "cli", "mail", "inbox", "--json"}, words(t, mailListCmd(skymail.FolderInbox)))
	require.Equal(t, []string{"skywire", "cli", "mail", "sent", "--json"}, words(t, mailListCmd(skymail.FolderSent)))
	require.Equal(t, []string{"skywire", "cli", "mail", "--json"}, words(t, mailStatusCmd))
}

func TestAddressedToFallsBackToDefault(t *testing.T) {
	m := &skymail.Rendered{To: "someone@other.skynet", Cc: "x@mine.dmsg"}
	require.Equal(t, "x@mine.dmsg", addressedTo(m, "mail@mine.skynet"), "Cc counts, and .dmsg is the same PK")
	require.Equal(t, "", addressedTo(&skymail.Rendered{To: "a@other.skynet"}, "mail@mine.skynet"), "not ours: default From")
	require.Equal(t, "", addressedTo(m, ""))
}

func TestSendCmdCarriesAttachments(t *testing.T) {
	got := words(t, mailSendCmd(skymail.Outgoing{
		To: []string{"b@x.skynet"}, Subject: "s", Body: "b",
		Attachments: []skymail.OutgoingAttachment{{Name: "it's.bin", Data: []byte{0, 255}}},
	}))
	require.Contains(t, got, "it's.bin=AP8=")
}

func TestAttachmentAndSettingsCmds(t *testing.T) {
	require.Equal(t, []string{"skywire", "cli", "mail", "attachment", "--sent", "--json", "--", "id", "2"},
		words(t, mailAttachmentCmd(skymail.FolderSent, "id", 2)))
	require.Equal(t, []string{"skywire", "cli", "mail", "settings", "--json"}, words(t, mailSettingsCmd(nil)))
	require.Equal(t, []string{"skywire", "cli", "mail", "settings", "--json", "--", "max_age=7d", "enable=true"},
		words(t, mailSettingsCmd([]string{"max_age=7d", "enable=true"})))
}

func TestFmtLimits(t *testing.T) {
	require.Equal(t, "16MiB", fmtSize(16<<20))
	require.Equal(t, "1.5MiB", fmtSize(3<<19))
	require.Equal(t, "2KiB", fmtSize(2048))
	require.Equal(t, "none", fmtSize(-1))
	require.Equal(t, "7d", fmtAge(7*24*time.Hour))
	require.Equal(t, "36h0m0s", fmtAge(36*time.Hour))
	require.Equal(t, "none", fmtAge(-1))
}
