// Package climail cmd/skywire-cli/commands/mail/mailbox.go c4-vis-cli
//
// `skywire cli mail inbox|sent|read|send|rm|whitelist` — the visor's own
// mailbox (pkg/skymail): mail to <anything>@<base32-pk>.skynet or .dmsg,
// delivered straight to the recipient visor's port 25.
package climail

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

var (
	readSent  bool
	readRaw   bool
	rmSent    bool
	sendSubj  string
	sendBody  string
	sendFrom  string
	sendCc    []string
	sendReply string
	sendFiles []string
	attSent   bool
	attOut    string
)

func init() {
	RootCmd.AddCommand(inboxCmd, sentCmd, readCmd, sendCmd, rmCmd, whitelistCmd, attachmentCmd)
	readCmd.Flags().BoolVar(&readSent, "sent", false, "read from Sent instead of the inbox")
	readCmd.Flags().BoolVar(&readRaw, "raw", false, "print the message exactly as stored")
	rmCmd.Flags().BoolVar(&rmSent, "sent", false, "remove from Sent instead of the inbox")
	sendCmd.Flags().StringVarP(&sendSubj, "subject", "s", "", "subject")
	sendCmd.Flags().StringVarP(&sendBody, "message", "m", "", "body (default: read from stdin)")
	sendCmd.Flags().StringVar(&sendFrom, "from", "", "local part of your address (default \"mail\")")
	sendCmd.Flags().StringSliceVar(&sendCc, "cc", nil, "carbon-copy recipients")
	sendCmd.Flags().StringVar(&sendReply, "in-reply-to", "", "Message-ID this replies to")
	sendCmd.Flags().StringArrayVar(&sendFiles, "attach", nil, "attach a file (repeatable)")
	attachmentCmd.Flags().BoolVar(&attSent, "sent", false, "from Sent instead of the inbox")
	attachmentCmd.Flags().StringVarP(&attOut, "output", "o", "", "file to write (default: the attachment's own name; - for stdout)")
}

func mailClient(cmd *cobra.Command) visorapi.API {
	c, err := clirpc.Client(cmd.Flags())
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	return c
}

func folderOf(sent bool) string {
	if sent {
		return skymail.FolderSent
	}
	return skymail.FolderInbox
}

var inboxCmd = &cobra.Command{
	Use:   "inbox",
	Short: "List the mailbox, newest first",
	Long: `List the mailbox, newest first.

A "*" marks unread mail. "verified" means the From address names the
same PK the transport authenticated, so the sender cannot have forged
it; "PK <hex>" shows who actually delivered mail whose From says
otherwise.`,
	Args: cobra.NoArgs,
	Run:  func(cmd *cobra.Command, _ []string) { listFolder(cmd, skymail.FolderInbox) },
}

var sentCmd = &cobra.Command{
	Use:   "sent",
	Short: "List sent mail, newest first",
	Args:  cobra.NoArgs,
	Run:   func(cmd *cobra.Command, _ []string) { listFolder(cmd, skymail.FolderSent) },
}

func listFolder(cmd *cobra.Command, folder string) {
	msgs, err := mailClient(cmd).MailList(folder)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	internal.PrintOutput(cmd.Flags(), msgs, renderList(msgs, folder == skymail.FolderInbox))
}

func renderList(msgs []skymail.Summary, inbox bool) string {
	if len(msgs) == 0 {
		return "(empty)\n"
	}
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	who := "from"
	if !inbox {
		who = "to"
	}
	fmt.Fprintf(w, " \tid\tdate\t%s\tsubject\tsender\n", who) //nolint:errcheck,gosec
	for _, m := range msgs {
		mark := " "
		if !m.Seen {
			mark = "*"
		}
		party, sender := m.From, ""
		if !inbox {
			party = m.To
		} else if m.FromVerified {
			sender = "verified"
		} else if m.PeerPK != "" {
			sender = "PK " + m.PeerPK
		}
		date := "-"
		if !m.Date.IsZero() {
			date = m.Date.Local().Format(time.DateTime)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", mark, m.ID, date, party, m.Subject, sender) //nolint:errcheck,gosec
	}
	_ = w.Flush() //nolint:errcheck,gosec
	return buf.String()
}

var readCmd = &cobra.Command{
	Use:   "read <id>",
	Short: "Show a message and mark it read",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c := mailClient(cmd)
		folder := folderOf(readSent)
		if readRaw {
			raw, err := c.MailRaw(folder, args[0])
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			internal.PrintOutput(cmd.Flags(), string(raw), string(raw))
			return
		}
		m, err := c.MailRead(folder, args[0])
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), m, renderMessage(m))
	},
}

func renderMessage(m *skymail.Rendered) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From:    %s\n", m.From)
	switch {
	case m.Verified:
		fmt.Fprintf(&b, "         (verified: delivered by %s)\n", m.PeerPK)
	case m.PeerPK != "":
		fmt.Fprintf(&b, "         (NOT verified: delivered by %s)\n", m.PeerPK)
	}
	fmt.Fprintf(&b, "To:      %s\n", m.To)
	if m.Cc != "" {
		fmt.Fprintf(&b, "Cc:      %s\n", m.Cc)
	}
	fmt.Fprintf(&b, "Date:    %s\nSubject: %s\n", m.Date, m.Subject)
	for i, a := range m.Attachments {
		fmt.Fprintf(&b, "Attachment %d: %s (%s, %d bytes)\n", i, a.Name, a.ContentType, a.Size)
	}
	if len(m.Attachments) > 0 {
		b.WriteString("         (save one: skywire cli mail attachment <id> <n>)\n")
	}
	if m.FromHTML {
		b.WriteString("(HTML-only message, shown as text)\n")
	}
	b.WriteString("\n")
	b.WriteString(strings.ReplaceAll(m.Text, "\r\n", "\n"))
	if !strings.HasSuffix(m.Text, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

var sendCmd = &cobra.Command{
	Use:   "send <to>...",
	Short: "Send mail now to skywire addresses",
	Long: `Send mail now to skywire addresses.

Recipients are user@<base32-pk>.skynet or .dmsg, or
user@<host>.<base32-pk>.skynet for a mailbox behind a Postfix bridge.
` + "`skywire cli visor pk dnslabel`" + ` gives the base32 form of a PK.

Delivery happens now: a recipient whose visor is offline gets an error
and nothing is queued. The body is read from stdin unless -m is given.

Examples:
  skywire cli mail send bob@<base32-pk>.skynet -s hello -m "hi bob"
  echo "hi" | skywire cli mail send bob@<base32-pk>.dmsg -s hello`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		body := sendBody
		if !cmd.Flags().Changed("message") {
			raw, err := io.ReadAll(os.Stdin)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("read body: %w", err))
			}
			body = string(raw)
		}
		att, err := readAttachments(sendFiles)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		res, err := mailClient(cmd).MailSend(skymail.Outgoing{
			From: sendFrom, To: args, Cc: sendCc, Subject: sendSubj, Body: body, InReplyTo: sendReply,
			Attachments: att,
		})
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		out := renderSend(res)
		if delivered(res) == 0 {
			// Per-recipient reasons are the useful part of a failure.
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("not delivered to any recipient\n%s", out))
		}
		internal.PrintOutput(cmd.Flags(), res, out)
	},
}

func delivered(res *skymail.SendResult) int {
	n := 0
	for _, r := range res.Recipients {
		if r.Err == "" {
			n++
		}
	}
	return n
}

func renderSend(res *skymail.SendResult) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	for _, r := range res.Recipients {
		if r.Err == "" {
			fmt.Fprintf(&b, "delivered  %s (via %s)\n", r.Rcpt, r.Via)
		} else {
			fmt.Fprintf(&b, "FAILED     %s: %s\n", r.Rcpt, r.Err)
		}
	}
	if res.ID == "" && delivered(res) > 0 {
		b.WriteString("(no copy kept in Sent: the mailbox is full)\n")
	}
	return b.String()
}

var rmCmd = &cobra.Command{
	Use:   "rm <id>...",
	Short: "Delete messages",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c := mailClient(cmd)
		for _, id := range args {
			if err := c.MailDelete(folderOf(rmSent), id); err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("%s: %w", id, err))
			}
		}
		internal.PrintOutput(cmd.Flags(), map[string]any{"deleted": args}, fmt.Sprintf("deleted %d\n", len(args)))
	},
}

var whitelistCmd = &cobra.Command{
	Use:   "whitelist [add|rm|clear] [pk]...",
	Short: "Show or change which PKs may deliver mail",
	Long: `Show or change which PKs may deliver mail to this visor.

An empty whitelist accepts mail from every PK.

Examples:
  skywire cli mail whitelist               # show
  skywire cli mail whitelist add <pk>...
  skywire cli mail whitelist rm <pk>...
  skywire cli mail whitelist clear         # accept everyone`,
	Run: func(cmd *cobra.Command, args []string) {
		c := mailClient(cmd)
		st, err := c.MailStatus()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if len(args) > 0 {
			wl, err := editWhitelist(st.Whitelist, args[0], args[1:])
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			if err := c.MailSetWhitelist(wl); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			st.Whitelist = wl
		}
		internal.PrintOutput(cmd.Flags(), st.Whitelist, renderWhitelist(st.Whitelist))
	},
}

func editWhitelist(cur []cipher.PubKey, op string, args []string) ([]cipher.PubKey, error) {
	parse := func() ([]cipher.PubKey, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("%s needs at least one PK", op)
		}
		out := make([]cipher.PubKey, 0, len(args))
		for _, a := range args {
			var pk cipher.PubKey
			if err := pk.Set(a); err != nil {
				return nil, fmt.Errorf("%q: %w", a, err)
			}
			out = append(out, pk)
		}
		return out, nil
	}
	switch op {
	case "clear":
		return []cipher.PubKey{}, nil
	case "add":
		pks, err := parse()
		if err != nil {
			return nil, err
		}
		seen := map[cipher.PubKey]bool{}
		out := []cipher.PubKey{}
		for _, pk := range append(append([]cipher.PubKey{}, cur...), pks...) {
			if !seen[pk] {
				seen[pk] = true
				out = append(out, pk)
			}
		}
		return out, nil
	case "rm":
		pks, err := parse()
		if err != nil {
			return nil, err
		}
		drop := map[cipher.PubKey]bool{}
		for _, pk := range pks {
			drop[pk] = true
		}
		out := []cipher.PubKey{}
		for _, pk := range cur {
			if !drop[pk] {
				out = append(out, pk)
			}
		}
		return out, nil
	}
	return nil, errors.New(`want "add", "rm" or "clear"`)
}

func renderWhitelist(pks []cipher.PubKey) string {
	if len(pks) == 0 {
		return "(empty: mail from every PK is accepted)\n"
	}
	var b strings.Builder
	for _, pk := range pks {
		b.WriteString(pk.Hex())
		b.WriteByte('\n')
	}
	return b.String()
}

// renderMailbox is the mailbox part of bare `mail`.
func renderMailbox(st *visorapi.MailStatus) string {
	if st == nil {
		return ""
	}
	if !st.Running {
		return fmt.Sprintf("mailbox: not running (%s)\n", st.Reason)
	}
	wl := "every PK"
	if n := len(st.Whitelist); n > 0 {
		wl = fmt.Sprintf("%d whitelisted PK(s)", n)
	}
	return fmt.Sprintf("mailbox: %d message(s), %d unread; accepts mail from %s\n  %s\n  %s\n  (any local part works; maildir %s)\n  %s of %s used; mail over %s is refused, mail older than %s is deleted\n",
		st.Total, st.Unread, wl, st.Address, st.AddressDmsg, st.Dir,
		skymail.FormatSize(st.Usage), skymail.FormatSize(st.Limits.MaxTotalSize), skymail.FormatSize(st.Limits.MaxMessageSize), skymail.FormatAge(st.Limits.MaxAge))
}
