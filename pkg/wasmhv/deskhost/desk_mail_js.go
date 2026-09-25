//go:build js && wasm

// Package deskhost pkg/wasmhv/deskhost/desk_mail_js.go c4-wasm-desk
//
// The ☰ mail app: the tab visor's own mailbox (pkg/skymail). Mail to
// <anything>@<base32-pk>.skynet arrives over skywire on port 25 and is
// kept in the tab's filesystem; sending dials the recipient's visor
// directly. Every action is a `skywire cli mail` command (mailcmd.go),
// so this window can do exactly what the terminal can, and no more.
package deskhost

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"github.com/0magnet/desk"

	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// mailRefresh is how often an open list looks for new mail.
const mailRefresh = 15 * time.Second

func registerMailApp() {
	desk.Register(desk.App{
		Name: "mail", Title: "mail",
		Help:  "e-mail over skywire: this tab's mailbox",
		Width: 860, Height: 560,
		Open: func(_ []string) (desk.Pane, error) { return &mailPane{}, nil },
	})
}

type mailPane struct {
	mu     sync.Mutex
	doc    js.Value
	body   js.Value // the view area
	status js.Value // one line of state or error
	addr   js.Value
	folder string
	own    string // this mailbox's default address
	usage  string // "1.2MiB of 16MiB"
	view   string // "list" while the list is showing; refresh only then
	stop   chan struct{}
	funcs  []js.Func
}

func (p *mailPane) Mount(el js.Value) error {
	p.doc = js.Global().Get("document")
	p.folder = skymail.FolderInbox
	p.stop = make(chan struct{})
	root := paneEl(p.doc, "div", paneCSS+";display:flex;flex-direction:column;padding:0", "")

	bar := paneEl(p.doc, "div", "display:flex;flex-wrap:wrap;align-items:center;gap:2px;padding:8px 12px;border-bottom:1px solid #2a2535", "")
	bar.Call("appendChild", paneButton(p.doc, "Inbox", func() { p.showList(skymail.FolderInbox) }))
	bar.Call("appendChild", paneButton(p.doc, "Sent", func() { p.showList(skymail.FolderSent) }))
	bar.Call("appendChild", paneButton(p.doc, "Compose", func() { p.showCompose(skymail.Outgoing{}) }))
	bar.Call("appendChild", paneButton(p.doc, "Whitelist", p.showWhitelist))
	bar.Call("appendChild", paneButton(p.doc, "Settings", p.showSettings))
	p.addr = paneEl(p.doc, "span", "margin-left:auto;font:12px ui-monospace,monospace;color:#9aa3b2;word-break:break-all;user-select:all", "")
	bar.Call("appendChild", p.addr)
	root.Call("appendChild", bar)

	p.status = paneEl(p.doc, "div", "padding:4px 12px;font-size:12px;color:#9aa3b2;min-height:18px", "")
	root.Call("appendChild", p.status)
	p.body = paneEl(p.doc, "div", "flex:1;overflow:auto;padding:0 12px 12px", "")
	root.Call("appendChild", p.body)
	el.Call("appendChild", root)

	go p.loadAddress()
	go p.showList(skymail.FolderInbox)
	go p.refreshLoop()
	return nil
}

func (p *mailPane) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stop != nil {
		close(p.stop)
		p.stop = nil
	}
	for _, f := range p.funcs {
		f.Release()
	}
	p.funcs = nil
}

func (p *mailPane) refreshLoop() {
	t := time.NewTicker(mailRefresh)
	defer t.Stop()
	p.mu.Lock()
	stop := p.stop
	p.mu.Unlock()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			p.mu.Lock()
			listing, folder := p.view == "list", p.folder
			p.mu.Unlock()
			if listing {
				p.showList(folder)
			}
		}
	}
}

func (p *mailPane) setStatus(format string, args ...any) {
	p.status.Set("textContent", fmt.Sprintf(format, args...))
}

func (p *mailPane) setView(name string) {
	p.mu.Lock()
	p.view = name
	// The rows being replaced own these; the list redraws every refresh.
	for _, f := range p.funcs {
		f.Release()
	}
	p.funcs = nil
	p.mu.Unlock()
	p.body.Set("innerHTML", "")
}

// run executes one CLI command against the tab's visor and decodes its
// JSON output into v.
func (p *mailPane) run(cmd string, v any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := runHeadless(ctx, cmd)
	return cliResult(out, err, v)
}

func (p *mailPane) loadAddress() {
	var st struct {
		Mailbox *visorapi.MailStatus `json:"mailbox"`
	}
	if err := p.run(mailStatusCmd, &st); err != nil || st.Mailbox == nil {
		p.setStatus("mailbox: %v", err)
		return
	}
	if !st.Mailbox.Running {
		p.setStatus("mailbox not running: %s", st.Mailbox.Reason)
		return
	}
	p.mu.Lock()
	p.own = st.Mailbox.Address
	p.usage = fmtSize(st.Mailbox.Usage) + " of " + fmtSize(st.Mailbox.Limits.MaxTotalSize)
	p.mu.Unlock()
	p.addr.Set("textContent", st.Mailbox.Address)
	p.addr.Set("title", "your address (any local part works); also "+st.Mailbox.AddressDmsg)
}

func (p *mailPane) showList(folder string) {
	p.mu.Lock()
	p.folder = folder
	p.mu.Unlock()
	var msgs []skymail.Summary
	if err := p.run(mailListCmd(folder), &msgs); err != nil {
		p.setStatus("%s: %v", folder, err)
		return
	}
	p.setView("list")
	unread := 0
	for _, m := range msgs {
		if !m.Seen {
			unread++
		}
	}
	if folder == skymail.FolderInbox {
		p.loadAddress()
		p.mu.Lock()
		usage := p.usage
		p.mu.Unlock()
		p.setStatus("Inbox: %d message(s), %d unread · %s used · checked %s", len(msgs), unread, usage, time.Now().Format("15:04:05"))
	} else {
		p.setStatus("Sent: %d message(s)", len(msgs))
	}
	if len(msgs) == 0 {
		p.body.Call("appendChild", paneEl(p.doc, "p", "color:#9aa3b2", "No mail."))
		return
	}
	table := paneEl(p.doc, "table", "width:100%;border-collapse:collapse;font-size:13px", "")
	for _, m := range msgs {
		m := m
		weight := "400"
		if !m.Seen {
			weight = "700"
		}
		tr := paneEl(p.doc, "tr", "cursor:pointer;border-bottom:1px solid #221e2b;font-weight:"+weight, "")
		party := m.From
		if folder == skymail.FolderSent {
			party = "to " + m.To
		}
		badge, badgeColor := "", "#9aa3b2"
		switch {
		case folder == skymail.FolderSent:
		case m.FromVerified:
			badge, badgeColor = "✓", "#7fd18b"
		case m.PeerPK != "":
			badge, badgeColor = "?", "#e0b050"
		}
		date := ""
		if !m.Date.IsZero() {
			date = m.Date.Local().Format("Jan 2 15:04")
		}
		for _, c := range []struct{ text, css string }{
			{badge, "width:18px;color:" + badgeColor},
			{party, "padding:6px 8px;max-width:240px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap"},
			{m.Subject, "padding:6px 8px"},
			{date, "padding:6px 8px;white-space:nowrap;color:#9aa3b2;text-align:right"},
		} {
			tr.Call("appendChild", paneEl(p.doc, "td", c.css, c.text))
		}
		f := js.FuncOf(func(js.Value, []js.Value) any {
			go p.showMessage(folder, m.ID)
			return nil
		})
		p.keep(f)
		tr.Call("addEventListener", "click", f)
		table.Call("appendChild", tr)
	}
	p.body.Call("appendChild", table)
}

func (p *mailPane) keep(f js.Func) {
	p.mu.Lock()
	p.funcs = append(p.funcs, f)
	p.mu.Unlock()
}

func (p *mailPane) showMessage(folder, id string) {
	var m skymail.Rendered
	if err := p.run(mailReadCmd(folder, id), &m); err != nil {
		p.setStatus("read: %v", err)
		return
	}
	p.setView("message")
	p.setStatus("")
	head := paneEl(p.doc, "div", "padding:8px 0;border-bottom:1px solid #2a2535;font-size:13px", "")
	line := func(k, v, color string) {
		if v == "" {
			return
		}
		d := paneEl(p.doc, "div", "word-break:break-all;"+color, "")
		d.Call("appendChild", paneEl(p.doc, "b", "display:inline-block;width:72px;color:#9aa3b2", k))
		d.Call("appendChild", p.doc.Call("createTextNode", v))
		head.Call("appendChild", d)
	}
	line("From", m.From, "")
	switch {
	case m.Verified:
		line("", "✓ verified: delivered by "+m.PeerPK, "color:#7fd18b")
	case m.PeerPK != "":
		line("", "? not verified: the From address is not "+m.PeerPK+", which delivered it", "color:#e0b050")
	}
	line("To", m.To, "")
	line("Cc", m.Cc, "")
	line("Date", m.Date, "")
	line("Subject", m.Subject, "font-weight:700")
	p.body.Call("appendChild", head)
	for i, a := range m.Attachments {
		i := i
		row := paneEl(p.doc, "div", "display:flex;align-items:center;gap:8px;font-size:13px;color:#9aa3b2;padding:2px 0", "")
		row.Call("appendChild", paneEl(p.doc, "span", "flex:1;word-break:break-all",
			fmt.Sprintf("📎 %s (%s, %s)", a.Name, a.ContentType, fmtSize(int64(a.Size)))))
		row.Call("appendChild", paneButton(p.doc, "Download", func() { p.download(folder, id, i) }))
		p.body.Call("appendChild", row)
	}

	acts := paneEl(p.doc, "div", "padding:6px 0", "")
	if folder == skymail.FolderInbox {
		acts.Call("appendChild", paneButton(p.doc, "Reply", func() {
			p.mu.Lock()
			own := p.own
			p.mu.Unlock()
			p.showCompose(replyTo(&m, own))
		}))
	}
	acts.Call("appendChild", paneButton(p.doc, "Delete", func() {
		if err := p.run(mailRmCmd(folder, id), nil); err != nil {
			p.setStatus("delete: %v", err)
			return
		}
		p.showList(folder)
	}))
	acts.Call("appendChild", paneButton(p.doc, "Back", func() { p.showList(folder) }))
	p.body.Call("appendChild", acts)

	text := strings.ReplaceAll(m.Text, "\r\n", "\n")
	if m.FromHTML {
		text = "(HTML-only message, shown as text)\n\n" + text
	}
	// textContent, never innerHTML: nothing a message says can run here.
	p.body.Call("appendChild", paneEl(p.doc, "pre",
		"white-space:pre-wrap;word-break:break-word;font:13px/1.5 ui-monospace,monospace;margin:8px 0", text))
}

func (p *mailPane) showCompose(draft skymail.Outgoing) {
	p.setView("compose")
	p.setStatus("Recipients: user@<base32-pk>.skynet or .dmsg. Delivery is immediate; nothing is queued.")
	if draft.From != "" {
		p.setStatus("Replying as %s. Delivery is immediate; nothing is queued.", draft.From)
	}
	field := func(label, value string, rows int) js.Value {
		wrap := paneEl(p.doc, "label", "display:block;margin:6px 0;font-size:12px;color:#9aa3b2", label)
		var in js.Value
		css := "display:block;width:100%;box-sizing:border-box;margin-top:2px;padding:6px;font:13px ui-monospace,monospace;background:#0f0d15;color:#e6e9ee;border:1px solid #2a2535"
		if rows > 0 {
			in = paneEl(p.doc, "textarea", css+";resize:vertical", "")
			in.Set("rows", rows)
		} else {
			in = paneEl(p.doc, "input", css, "")
		}
		in.Set("value", value)
		wrap.Call("appendChild", in)
		p.body.Call("appendChild", wrap)
		return in
	}
	to := field("To", strings.Join(draft.To, ", "), 0)
	cc := field("Cc", strings.Join(draft.Cc, ", "), 0)
	subj := field("Subject", draft.Subject, 0)
	body := field("Message", draft.Body, 14)
	files := paneEl(p.doc, "input", "margin:6px 0;color:#9aa3b2", "")
	files.Set("type", "file")
	files.Set("multiple", true)
	p.body.Call("appendChild", files)
	var sending bool
	p.body.Call("appendChild", paneButton(p.doc, "Send", func() {
		if sending {
			return
		}
		out := skymail.Outgoing{
			To: splitAddrs(to.Get("value").String()), Cc: splitAddrs(cc.Get("value").String()),
			Subject: subj.Get("value").String(), Body: body.Get("value").String(), InReplyTo: draft.InReplyTo,
			From: draft.From,
		}
		att, err := readPicked(files)
		if err != nil {
			p.setStatus("attachment: %v", err)
			return
		}
		out.Attachments = att
		if len(out.To) == 0 {
			p.setStatus("add a recipient")
			return
		}
		sending = true
		p.setStatus("sending…")
		var res skymail.SendResult
		err = p.run(mailSendCmd(out), &res)
		sending = false
		if err != nil {
			p.setStatus("not sent: %v", err)
			return
		}
		var failed, via []string
		for _, r := range res.Recipients {
			if r.Err != "" {
				failed = append(failed, r.Rcpt+": "+r.Err)
			} else {
				via = append(via, r.Via)
			}
		}
		p.showList(skymail.FolderSent)
		if len(failed) > 0 {
			p.setStatus("sent, but not to %s", strings.Join(failed, "; "))
		} else {
			p.setStatus("sent to %d recipient(s) via %s", len(res.Recipients), strings.Join(via, ", "))
		}
	}))
}

func (p *mailPane) showWhitelist() {
	var wl []string
	if err := p.run(mailWhitelistCmd("", nil), &wl); err != nil {
		p.setStatus("whitelist: %v", err)
		return
	}
	p.setView("whitelist")
	if len(wl) == 0 {
		p.setStatus("Whitelist empty: mail from every PK is accepted.")
	} else {
		p.setStatus("Only these PKs may deliver mail here.")
	}
	for _, pk := range wl {
		pk := pk
		row := paneEl(p.doc, "div", "display:flex;align-items:center;gap:8px;font:12px ui-monospace,monospace;word-break:break-all", "")
		row.Call("appendChild", paneEl(p.doc, "span", "flex:1", pk))
		row.Call("appendChild", paneButton(p.doc, "Remove", func() { p.editWhitelist("rm", pk) }))
		p.body.Call("appendChild", row)
	}
	in := paneEl(p.doc, "input", "width:100%;box-sizing:border-box;margin:10px 0 4px;padding:6px;font:12px ui-monospace,monospace;background:#0f0d15;color:#e6e9ee;border:1px solid #2a2535", "")
	in.Set("placeholder", "public key (66 hex chars)")
	p.body.Call("appendChild", in)
	p.body.Call("appendChild", paneButton(p.doc, "Add", func() { p.editWhitelist("add", strings.TrimSpace(in.Get("value").String())) }))
	if len(wl) > 0 {
		p.body.Call("appendChild", paneButton(p.doc, "Clear (accept everyone)", func() { p.editWhitelist("clear", "") }))
	}
}

func (p *mailPane) editWhitelist(op, pk string) {
	var pks []string
	if pk != "" {
		pks = []string{pk}
	}
	if err := p.run(mailWhitelistCmd(op, pks), nil); err != nil {
		p.setStatus("whitelist %s: %v", op, err)
		return
	}
	p.showWhitelist()
}

// download saves attachment n through the browser: a Blob and a link
// that is clicked once. The bytes never pass through the page's DOM.
func (p *mailPane) download(folder, id string, n int) {
	var a skymail.AttachmentData
	if err := p.run(mailAttachmentCmd(folder, id, n), &a); err != nil {
		p.setStatus("download: %v", err)
		return
	}
	arr := js.Global().Get("Uint8Array").New(len(a.Data))
	js.CopyBytesToJS(arr, a.Data)
	ct := a.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	blob := js.Global().Get("Blob").New([]any{arr}, map[string]any{"type": ct})
	url := js.Global().Get("URL").Call("createObjectURL", blob)
	link := p.doc.Call("createElement", "a")
	link.Set("href", url)
	link.Set("download", a.Name)
	p.doc.Get("body").Call("appendChild", link)
	link.Call("click")
	link.Call("remove")
	js.Global().Get("URL").Call("revokeObjectURL", url)
	p.setStatus("saved %s (%s)", a.Name, fmtSize(int64(len(a.Data))))
}

// readPicked reads the files chosen in a file input.
func readPicked(input js.Value) ([]skymail.OutgoingAttachment, error) {
	list := input.Get("files")
	var out []skymail.OutgoingAttachment
	for i := 0; i < list.Get("length").Int(); i++ {
		f := list.Index(i)
		buf, err := jsAwait(f.Call("arrayBuffer"))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Get("name").String(), err)
		}
		u8 := js.Global().Get("Uint8Array").New(buf)
		data := make([]byte, u8.Get("length").Int())
		js.CopyBytesToGo(data, u8)
		out = append(out, skymail.OutgoingAttachment{
			Name: f.Get("name").String(), ContentType: f.Get("type").String(), Data: data,
		})
	}
	return out, nil
}

// showSettings edits the live settings: they apply at once and are saved
// to the visor config.
func (p *mailPane) showSettings() {
	var st visorapi.MailStatus
	if err := p.run(mailSettingsCmd(nil), &st); err != nil {
		p.setStatus("settings: %v", err)
		return
	}
	p.setView("settings")
	running := "running"
	if !st.Running {
		running = "not running: " + st.Reason
	}
	p.setStatus("Mailbox %s · %s of %s used. Changes apply at once.", running, fmtSize(st.Usage), fmtSize(st.Limits.MaxTotalSize))
	row := func(label, help string, in js.Value) {
		wrap := paneEl(p.doc, "label", "display:block;margin:8px 0;font-size:12px;color:#9aa3b2", label)
		wrap.Call("appendChild", in)
		wrap.Call("appendChild", paneEl(p.doc, "div", "font-size:11px;color:#6f7787", help))
		p.body.Call("appendChild", wrap)
	}
	input := func(v string) js.Value {
		in := paneEl(p.doc, "input", "display:block;width:220px;margin-top:2px;padding:6px;font:13px ui-monospace,monospace;background:#0f0d15;color:#e6e9ee;border:1px solid #2a2535", "")
		in.Set("value", v)
		return in
	}
	enable := paneEl(p.doc, "input", "margin:4px 0", "")
	enable.Set("type", "checkbox")
	enable.Set("checked", st.Enabled)
	row("Receive mail", "off closes port 25; stored mail is kept", enable)
	msg := input(fmtSize(st.Limits.MaxMessageSize))
	row("Largest message", "e.g. 1MiB; \"none\" for no limit, \"default\" for 1MiB", msg)
	total := input(fmtSize(st.Limits.MaxTotalSize))
	row("Mailbox size", "Inbox and Sent together; a full mailbox refuses mail. e.g. 16MiB", total)
	age := input(fmtAge(st.Limits.MaxAge))
	row("Delete mail after", "e.g. 7d or 36h; \"none\" keeps mail forever", age)
	p.body.Call("appendChild", paneButton(p.doc, "Save", func() {
		kvs := []string{
			fmt.Sprintf("enable=%v", enable.Get("checked").Bool()),
			"max_message_size=" + strings.TrimSpace(msg.Get("value").String()),
			"max_total_size=" + strings.TrimSpace(total.Get("value").String()),
			"max_age=" + strings.TrimSpace(age.Get("value").String()),
		}
		if err := p.run(mailSettingsCmd(kvs), nil); err != nil {
			p.setStatus("not saved: %v", err)
			return
		}
		p.showSettings()
		p.setStatus("saved")
	}))
}
