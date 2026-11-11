package commands

import (
	"net/mail"
	"strings"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/skymail"
)

// notify announces new mail under the app's own name, so a phone files it
// in a channel of its own.
func notify(cl *app.Client, d skymail.Delivered) {
	subject := d.Subject
	if subject == "" {
		subject = "(no subject)"
	}
	if err := cl.Notify(appserver.NotifyReq{Title: mailSender(d.From), Body: subject, Tag: d.ID}); err != nil {
		cl.Log().WithError(err).Debug("skymail: notification not delivered")
	}
}

// mailSender is a sender short enough for a notification title: the display
// name when there is one, else the address with its 53-character key cut.
func mailSender(from string) string {
	a, err := mail.ParseAddress(from)
	if err != nil {
		return from
	}
	if a.Name != "" {
		return a.Name
	}
	local, domain, ok := strings.Cut(a.Address, "@")
	if !ok || len(domain) <= 16 {
		return a.Address
	}
	return local + "@" + domain[:8] + "…" + domain[strings.LastIndex(domain, "."):]
}
