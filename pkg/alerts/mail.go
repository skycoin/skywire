// Package alerts pkg/alerts/mail.go c3-vis-core
package alerts

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Sender delivers events by mail, webhook and Web Push.
type Sender struct {
	// Watcher is the public key of the visor doing the watching.
	Watcher cipher.PubKey
	// Hostname is the watcher's host name, for the default sender address.
	Hostname string
	// LookupMX and MXPort are overridable for tests. Defaults are the
	// system resolver and 25.
	LookupMX func(ctx context.Context, domain string) ([]*MX, error)
	MXPort   int
	// TLSConfig is used for STARTTLS and implicit TLS. Nil verifies the
	// server name against the system roots.
	TLSConfig *tls.Config
	// HTTPClient posts webhooks and pushes. Nil is a client with a 15s
	// timeout.
	HTTPClient *http.Client
	// VAPID signs Web Push requests (RFC 8292). Nil sends no pushes.
	VAPID *ecdsa.PrivateKey
}

// MX is one mail exchanger of a domain, as in net.MX.
type MX struct {
	Host string
	Pref uint16
}

// Result is the outcome of one delivery channel.
type Result struct {
	Channel string `json:"channel"`
	Target  string `json:"target"`
	Err     string `json:"error,omitempty"`
	// gone is the id of a push subscription the push service dropped.
	gone string
}

// Send delivers ev to every configured channel and reports each one.
func (s *Sender) Send(ctx context.Context, set Settings, ev Event) []Result {
	var out []Result
	if len(set.EmailTo) > 0 {
		msg := s.Format(set, ev)
		for _, rcpt := range set.EmailTo {
			err := s.sendMail(ctx, set, rcpt, msg)
			out = append(out, result("email", rcpt, err))
		}
	}
	if set.Webhook != "" {
		out = append(out, result("webhook", set.Webhook, s.postWebhook(ctx, set.Webhook, ev)))
	}
	if len(set.Push) > 0 {
		payload := s.pushPayload(ev)
		urgent := ev.Kind == KindOffline || ev.Kind == KindLinkLost || ev.Kind == KindConflict
		for _, sub := range set.Push {
			gone, err := s.sendPush(ctx, pushSubject(set), sub, payload, urgent)
			r := result("push", sub.name(), err)
			if gone {
				r.gone = sub.ID
			}
			out = append(out, r)
		}
	}
	return out
}

func result(ch, target string, err error) Result {
	r := Result{Channel: ch, Target: target}
	if err != nil {
		r.Err = err.Error()
	}
	return r
}

func (s *Sender) from(set Settings) string {
	if set.From != "" {
		return set.From
	}
	if set.SMTP != nil && strings.Contains(set.SMTP.Username, "@") {
		return set.SMTP.Username
	}
	h := s.Hostname
	if h == "" {
		h = "localhost"
	}
	return "skywire-alerts@" + h
}

// Subject is the mail subject for ev.
func Subject(ev Event) string {
	name := displayName(ev.Target)
	switch {
	case ev.Kind == KindConflict:
		return fmt.Sprintf("[skywire] IP conflict: %d visors at site %s share LAN IP %s", len(ev.Members), ev.Site, ev.Target.LocalIP)
	case ev.Scope == ScopeSite && ev.Kind == KindOffline:
		return fmt.Sprintf("[skywire] site %s: all %d visors offline", ev.Site, len(ev.Members))
	case ev.Scope == ScopeSite && ev.Kind == KindOnline:
		return fmt.Sprintf("[skywire] site %s is back online", ev.Site)
	case ev.Kind == KindLinkLost:
		return fmt.Sprintf("[skywire] visor %s lost its connection to the hypervisor", name)
	case ev.Kind == KindOnline && ev.Diagnosis == DiagLinkRestored:
		return fmt.Sprintf("[skywire] visor %s reconnected to the hypervisor", name)
	case ev.Kind == KindOffline:
		return fmt.Sprintf("[skywire] visor %s is offline", name)
	case ev.Kind == KindOnline:
		return fmt.Sprintf("[skywire] visor %s is back online", name)
	case ev.Kind == KindStorage:
		return fmt.Sprintf("[skywire] visor %s storage warning", name)
	default:
		return "[skywire] test alert"
	}
}

// Body is the plain text mail body for ev.
func (s *Sender) Body(ev Event) string {
	var b strings.Builder
	site := ev.Scope == ScopeSite
	switch {
	case ev.Kind == KindConflict:
		b.WriteString("Two or more visors you watch at one site report the same LAN IP. Expect both to flap until one gets a new address.\n\n")
	case site && ev.Kind == KindOffline:
		b.WriteString("Every visor you watch behind one public IP went offline.\n\n")
	case site && ev.Kind == KindOnline:
		b.WriteString("A site you watch is back online.\n\n")
	case ev.Kind == KindLinkLost:
		b.WriteString("A visor you watch is up but has not reconnected to its hypervisor. Check the network path between them.\n\n")
	case ev.Kind == KindOnline && ev.Diagnosis == DiagLinkRestored:
		b.WriteString("A visor you watch reconnected to its hypervisor.\n\n")
	case ev.Kind == KindOffline:
		b.WriteString("A visor you watch went offline.\n\n")
	case ev.Kind == KindOnline:
		b.WriteString("A visor you watch is back online.\n\n")
	case ev.Kind == KindStorage:
		b.WriteString("A visor you watch reports storage problems. Its SD card or disk may be failing.\n\n")
	default:
		b.WriteString("This is a test of your skywire visor alerts. Delivery works.\n\n")
	}
	line := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%-14s %s\n", k+":", v)
		}
	}
	if ev.Kind != KindTest {
		line("Kind", ev.Scope)
		if site {
			line("Site", ev.Site)
			line("LAN IP", ev.Target.LocalIP)
		} else {
			line("Hostname", ev.Target.Hostname)
			line("Public key", ev.Target.PK.String())
			line("Public IP", ev.Target.PublicIP)
			line("LAN IP", ev.Target.LocalIP)
			if !ev.Target.LastSeen.IsZero() {
				line("Last seen", ev.Target.LastSeen.UTC().Format(time.RFC3339))
			}
		}
		if !ev.Since.IsZero() {
			label := "Offline since"
			if ev.Kind == KindLinkLost || ev.Diagnosis == DiagLinkRestored {
				label = "Link lost"
			}
			line(label, ev.Since.UTC().Format(time.RFC3339))
			if ev.Kind == KindOnline {
				line("Down for", ev.At.Sub(ev.Since).Truncate(time.Second).String())
			}
		}
		line("Diagnosis", ev.Diagnosis)
		line("Recovery", ev.RecoveryReason)
		for _, w := range ev.Warnings {
			line("Warning", w)
		}
	}
	line("Time", ev.At.UTC().Format(time.RFC3339))
	line("Watched by", s.Watcher.String())
	if len(ev.Members) > 0 {
		b.WriteString("\nVisors:\n")
		for _, m := range ev.Members {
			fmt.Fprintf(&b, "  %s %s %s\n", m.PK, orDash(m.Hostname), orDash(m.LocalIP))
		}
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Format builds the full RFC 5322 message for ev.
func (s *Sender) Format(set Settings, ev Event) []byte {
	var b bytes.Buffer
	hdr := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	from := s.from(set)
	hdr("From", from)
	hdr("To", strings.Join(set.EmailTo, ", "))
	hdr("Subject", Subject(ev))
	hdr("Date", ev.At.UTC().Format(time.RFC1123Z))
	hdr("Message-ID", "<"+randomID()+"@"+domainOf(from)+">")
	hdr("MIME-Version", "1.0")
	hdr("Content-Type", "text/plain; charset=utf-8")
	hdr("Content-Transfer-Encoding", "8bit")
	hdr("Auto-Submitted", "auto-generated")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(s.Body(ev), "\n", "\r\n"))
	return b.Bytes()
}

func randomID() string {
	var r [12]byte
	_, _ = rand.Read(r[:]) //nolint:errcheck
	return hex.EncodeToString(r[:])
}

func domainOf(addr string) string {
	if i := strings.LastIndexByte(addr, '@'); i >= 0 {
		return strings.Trim(addr[i+1:], "> ")
	}
	return "localhost"
}

func (s *Sender) sendMail(ctx context.Context, set Settings, rcpt string, msg []byte) error {
	from := s.from(set)
	if set.SMTP != nil {
		return s.viaRelay(ctx, *set.SMTP, from, rcpt, msg)
	}
	return s.direct(ctx, from, rcpt, msg)
}

// direct delivers to the recipient domain's MX hosts in preference order,
// falling back to the domain itself (RFC 5321 section 5.1).
func (s *Sender) direct(ctx context.Context, from, rcpt string, msg []byte) error {
	domain := domainOf(rcpt)
	lookup := s.LookupMX
	if lookup == nil {
		lookup = lookupMX
	}
	mxs, err := lookup(ctx, domain)
	hosts := make([]string, 0, len(mxs))
	sort.SliceStable(mxs, func(i, j int) bool { return mxs[i].Pref < mxs[j].Pref })
	for _, mx := range mxs {
		hosts = append(hosts, strings.TrimSuffix(mx.Host, "."))
	}
	if len(hosts) == 0 {
		if err != nil && !isNotFound(err) {
			return fmt.Errorf("mx lookup for %s: %w", domain, err)
		}
		hosts = []string{domain}
	}
	port := s.MXPort
	if port == 0 {
		port = 25
	}
	var errs []error
	for _, h := range hosts {
		err := s.deliver(ctx, h, port, false, false, nil, from, rcpt, msg)
		if err == nil {
			return nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", h, err))
	}
	return errors.Join(errs...)
}

func isNotFound(err error) bool {
	var de *net.DNSError
	return errors.As(err, &de) && de.IsNotFound
}

func (s *Sender) viaRelay(ctx context.Context, r SMTP, from, rcpt string, msg []byte) error {
	port := r.Port
	if port == 0 {
		port = 587
		if !r.StartTLS {
			port = 25
		}
	}
	var auth smtp.Auth
	if r.Username != "" {
		auth = smtp.PlainAuth("", r.Username, r.Password, r.Host)
	}
	return s.deliver(ctx, r.Host, port, port == 465, r.StartTLS, auth, from, rcpt, msg)
}

// deliver runs one SMTP transaction. STARTTLS is used whenever the server
// offers it; requireTLS makes a server that does not offer it an error.
func (s *Sender) deliver(ctx context.Context, host string, port int, implicitTLS, requireTLS bool, auth smtp.Auth, from, rcpt string, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl) //nolint:errcheck
	}
	tlsConf := s.tlsConfig(host)
	if implicitTLS {
		conn = tls.Client(conn, tlsConf)
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close() //nolint:errcheck
		return err
	}
	defer c.Close() //nolint:errcheck
	if err := c.Hello(s.helloName()); err != nil {
		return err
	}
	if !implicitTLS {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsConf); err != nil {
				return fmt.Errorf("starttls: %w", err)
			}
		} else if requireTLS {
			return errors.New("server does not offer STARTTLS")
		}
	}
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(rcpt); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func (s *Sender) helloName() string {
	if s.Hostname != "" && !strings.ContainsAny(s.Hostname, " \t") {
		return s.Hostname
	}
	return "localhost"
}

func (s *Sender) tlsConfig(host string) *tls.Config {
	if s.TLSConfig != nil {
		c := s.TLSConfig.Clone()
		if c.ServerName == "" {
			c.ServerName = host
		}
		return c
	}
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
}

// WebhookPayload is the JSON body of a webhook POST.
type WebhookPayload struct {
	Event string `json:"event"`
	// Kind is "board" or "site".
	Kind           string    `json:"kind,omitempty"`
	PK             string    `json:"pk,omitempty"`
	Hostname       string    `json:"hostname,omitempty"`
	PublicIP       string    `json:"public_ip,omitempty"`
	LocalIP        string    `json:"local_ip,omitempty"`
	LastSeen       time.Time `json:"last_seen,omitzero"`
	OfflineSince   time.Time `json:"offline_since,omitzero"`
	At             time.Time `json:"at"`
	Diagnosis      string    `json:"diagnosis,omitempty"`
	RecoveryReason string    `json:"recovery_reason,omitempty"`
	Warnings       []string  `json:"warnings,omitempty"`
	// Members are the visors of a site event.
	Members []Target `json:"members,omitempty"`
	Watcher string   `json:"watcher"`
	Text    string   `json:"text"`
}

func (s *Sender) postWebhook(ctx context.Context, u string, ev Event) error {
	p := WebhookPayload{
		Event: ev.Kind, Kind: ev.Scope, Hostname: ev.Target.Hostname, PublicIP: ev.Target.PublicIP,
		LocalIP: ev.Target.LocalIP, LastSeen: ev.Target.LastSeen, OfflineSince: ev.Since, At: ev.At,
		Diagnosis: ev.Diagnosis, RecoveryReason: ev.RecoveryReason, Warnings: ev.Warnings,
		Members: ev.Members, Watcher: s.Watcher.String(), Text: Subject(ev),
	}
	if !ev.Target.PK.Null() {
		p.PK = ev.Target.PK.String()
	}
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	hc := s.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close() //nolint:errcheck
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webhook answered %s", resp.Status)
	}
	return nil
}
