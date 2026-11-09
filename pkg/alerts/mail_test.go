package alerts

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// sinkMail is one message an smtpSink accepted.
type sinkMail struct {
	From, To, Data, Auth string
	TLS                  bool
}

// smtpSink is a tiny SMTP server for tests: EHLO, optional STARTTLS and
// AUTH PLAIN, MAIL, RCPT, DATA, QUIT.
type smtpSink struct {
	ln   net.Listener
	tls  *tls.Config
	mu   sync.Mutex
	mail []sinkMail
}

func newSink(t *testing.T, tlsConf *tls.Config) *smtpSink {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := &smtpSink{ln: ln, tls: tlsConf}
	t.Cleanup(func() { _ = ln.Close() }) //nolint:errcheck
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func addrArg(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '>'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimPrefix(s, "<")
}

func (s *smtpSink) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *smtpSink) got() []sinkMail {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sinkMail(nil), s.mail...)
}

func (s *smtpSink) serve(c net.Conn) {
	defer c.Close() //nolint:errcheck
	r, w := bufio.NewReader(c), c
	say := func(l string) { _, _ = w.Write([]byte(l + "\r\n")) } //nolint:errcheck
	say("220 sink ESMTP")
	var m sinkMail
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			ext := []string{"250-sink", "250-AUTH PLAIN"}
			if s.tls != nil && !m.TLS {
				ext = append(ext, "250-STARTTLS")
			}
			for _, e := range ext {
				say(e)
			}
			say("250 8BITMIME")
		case cmd == "STARTTLS":
			say("220 go ahead")
			tc := tls.Server(c, s.tls)
			if tc.Handshake() != nil {
				return
			}
			c, r, w = tc, bufio.NewReader(tc), tc
			m.TLS = true
		case strings.HasPrefix(cmd, "AUTH PLAIN"):
			b, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len("AUTH PLAIN"):])) //nolint:errcheck
			m.Auth = string(b)
			say("235 ok")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			m.From = addrArg(line[len("MAIL FROM:"):])
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			m.To = addrArg(line[len("RCPT TO:"):])
			say("250 ok")
		case cmd == "DATA":
			say("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			m.Data = b.String()
			s.mu.Lock()
			s.mail = append(s.mail, m)
			s.mu.Unlock()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func selfSigned(t *testing.T) (*tls.Config, *x509.CertPool) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}, pool
}

func testEvent(kind string) Event {
	pk, _ := cipher.GenerateKeyPair()
	at := time.Date(2026, 11, 8, 12, 30, 0, 0, time.UTC)
	return Event{
		Kind: kind, At: at, Since: at.Add(-10 * time.Minute),
		Target: Target{PK: pk, Hostname: "site-b", PublicIP: "198.51.100.4", LastSeen: at.Add(-10 * time.Minute)},
	}
}

func TestFormat(t *testing.T) {
	watcher, _ := cipher.GenerateKeyPair()
	s := &Sender{Watcher: watcher, Hostname: "home"}
	set := Settings{EmailTo: []string{"ops@example.com", "me@example.org"}}
	ev := testEvent(KindOffline)
	msg := string(s.Format(set, ev))

	require.Contains(t, msg, "From: skywire-alerts@home\r\n")
	require.Contains(t, msg, "To: ops@example.com, me@example.org\r\n")
	require.Contains(t, msg, "Subject: [skywire] visor site-b is offline\r\n")
	require.Contains(t, msg, "Message-ID: <")
	require.Contains(t, msg, ev.Target.PK.String(), "the full public key")
	require.Contains(t, msg, watcher.String())
	require.Contains(t, msg, "198.51.100.4")
	require.Contains(t, msg, "Last seen:     2026-11-08T12:20:00Z")
	require.NotContains(t, strings.ReplaceAll(msg, "\r\n", ""), "\n", "every line ends in CRLF")

	on := testEvent(KindOnline)
	body := s.Body(on)
	require.Contains(t, Subject(on), "is back online")
	require.Contains(t, body, "Down for:      10m0s")

	noName := testEvent(KindOffline)
	noName.Target.Hostname = ""
	require.Contains(t, Subject(noName), noName.Target.PK.String())
	require.Equal(t, "[skywire] test alert", Subject(Event{Kind: KindTest}))

	set.From = "alerts@example.net"
	require.Contains(t, string(s.Format(set, ev)), "From: alerts@example.net\r\n")
}

func TestDirectDelivery(t *testing.T) {
	sink := newSink(t, nil)
	var asked []string
	s := &Sender{
		Hostname: "home",
		MXPort:   sink.port(),
		LookupMX: func(_ context.Context, d string) ([]*net.MX, error) {
			asked = append(asked, d)
			// The preferred host refuses, so delivery falls through.
			return []*net.MX{{Host: "127.0.0.1.", Pref: 20}, {Host: "127.0.0.2.", Pref: 10}}, nil
		},
	}
	ev := testEvent(KindOffline)
	res := s.Send(context.Background(), Settings{EmailTo: []string{"ops@example.com"}}, ev)
	require.Len(t, res, 1)
	require.Empty(t, res[0].Err)
	require.Equal(t, []string{"example.com"}, asked)

	got := sink.got()
	require.Len(t, got, 1)
	require.Equal(t, "skywire-alerts@home", got[0].From)
	require.Equal(t, "ops@example.com", got[0].To)
	require.Empty(t, got[0].Auth)
	require.Contains(t, got[0].Data, ev.Target.PK.String())
}

func TestRelayDeliveryWithStartTLSAndAuth(t *testing.T) {
	srvTLS, pool := selfSigned(t)
	sink := newSink(t, srvTLS)
	s := &Sender{
		Hostname:  "home",
		TLSConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		LookupMX: func(context.Context, string) ([]*net.MX, error) {
			t.Fatal("relay mode must not look up MX")
			return nil, nil
		},
	}
	set := Settings{
		EmailTo: []string{"ops@example.com"},
		SMTP:    &SMTP{Host: "127.0.0.1", Port: sink.port(), Username: "relayuser@example.net", Password: "pw", StartTLS: true},
	}
	res := s.Send(context.Background(), set, testEvent(KindOnline))
	require.Len(t, res, 1)
	require.Empty(t, res[0].Err)
	got := sink.got()
	require.Len(t, got, 1)
	require.True(t, got[0].TLS, "STARTTLS was used")
	require.Equal(t, "\x00relayuser@example.net\x00pw", got[0].Auth)
	require.Equal(t, "relayuser@example.net", got[0].From, "the relay login is the default sender")
	require.Contains(t, got[0].Data, "is back online")

	// A relay without STARTTLS is refused when StartTLS is required.
	plain := newSink(t, nil)
	set.SMTP.Port = plain.port()
	res = s.Send(context.Background(), set, testEvent(KindOnline))
	require.Contains(t, res[0].Err, "STARTTLS")
	require.Empty(t, plain.got())
}

func TestWebhook(t *testing.T) {
	var got WebhookPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
	}))
	defer srv.Close()
	s := &Sender{}
	ev := testEvent(KindOffline)
	res := s.Send(context.Background(), Settings{Webhook: srv.URL}, ev)
	require.Len(t, res, 1)
	require.Empty(t, res[0].Err)
	require.Equal(t, "offline", got.Event)
	require.Equal(t, ev.Target.PK.String(), got.PK)
	require.Equal(t, "site-b", got.Hostname)

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	res = s.Send(context.Background(), Settings{Webhook: bad.URL}, ev)
	require.Contains(t, res[0].Err, "500")
}

// TestServiceEndToEnd drives the whole path: liveness checks, the delay,
// one offline mail, one recovery mail, and the state across a restart.
func TestServiceEndToEnd(t *testing.T) {
	sink := newSink(t, nil)
	dir := t.TempDir()
	pk, _ := cipher.GenerateKeyPair()
	sender := &Sender{Hostname: "home", MXPort: sink.port(),
		LookupMX: func(context.Context, string) ([]*net.MX, error) { return []*net.MX{{Host: "127.0.0.1"}}, nil }}

	online := true
	var mu sync.Mutex
	targets := func(context.Context, Settings) []Target {
		mu.Lock()
		defer mu.Unlock()
		return []Target{{PK: pk, Hostname: "site-c", Online: online}}
	}
	svc, err := NewService(dir, nil, sender)
	require.NoError(t, err)
	svc.Targets = targets
	require.Error(t, svc.SetSettings(Settings{EmailTo: []string{"ops@example.com"}, OfflineAfter: Duration(30 * time.Second)}), "below the 1m minimum")
	require.NoError(t, svc.SetSettings(Settings{EmailTo: []string{"ops@example.com"}, OfflineAfter: Duration(time.Minute)}))

	ctx := context.Background()
	t0 := time.Now()
	svc.Tick(ctx, t0)
	mu.Lock()
	online = false
	mu.Unlock()
	svc.Tick(ctx, t0.Add(30*time.Second))
	svc.Tick(ctx, t0.Add(80*time.Second))
	require.Empty(t, sink.got(), "inside the delay")
	svc.Tick(ctx, t0.Add(91*time.Second))
	svc.Tick(ctx, t0.Add(120*time.Second))
	require.Len(t, sink.got(), 1)
	require.Contains(t, sink.got()[0].Data, "visor site-c is offline")
	st := svc.Status()
	require.Len(t, st.Watched, 1)
	require.True(t, st.Watched[0].Alerted)
	require.Equal(t, "1m0s", st.OfflineAfter)

	// A restart keeps the settings and the alerted state.
	svc2, err := NewService(dir, &Settings{EmailTo: []string{"ignored@example.com"}}, sender)
	require.NoError(t, err)
	require.Equal(t, []string{"ops@example.com"}, svc2.Settings().EmailTo, "runtime settings win over the config block")
	svc2.Targets = targets
	svc2.Tick(ctx, t0.Add(150*time.Second))
	require.Len(t, sink.got(), 1, "no repeat after a restart")
	mu.Lock()
	online = true
	mu.Unlock()
	svc2.Tick(ctx, t0.Add(180*time.Second))
	svc2.Tick(ctx, t0.Add(210*time.Second))
	require.Len(t, sink.got(), 2)
	require.Contains(t, sink.got()[1].Data, "is back online")

	res, err := svc2.Test(ctx)
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.Empty(t, res[0].Err)
	require.Contains(t, sink.got()[2].Data, "test of your skywire visor alerts")
}

func TestServiceRetriesFailedDelivery(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	pk, _ := cipher.GenerateKeyPair()
	sender := &Sender{MXPort: port,
		LookupMX: func(context.Context, string) ([]*net.MX, error) { return []*net.MX{{Host: "127.0.0.1"}}, nil }}
	svc, err := NewService("", &Settings{EmailTo: []string{"ops@example.com"}, OfflineAfter: Duration(time.Minute)}, sender)
	require.NoError(t, err)
	svc.Targets = func(context.Context, Settings) []Target { return []Target{{PK: pk}} }
	t0 := time.Now()
	svc.Tick(context.Background(), t0)
	svc.Tick(context.Background(), t0.Add(2*time.Minute))
	require.Equal(t, 1, svc.Status().Pending, "a failed alert waits for a retry")

	sink := newSinkOn(t, port)
	svc.Tick(context.Background(), t0.Add(3*time.Minute))
	require.Len(t, sink.got(), 1)
	require.Zero(t, svc.Status().Pending)
}

func newSinkOn(t *testing.T, port int) *smtpSink {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	require.NoError(t, err)
	s := &smtpSink{ln: ln}
	t.Cleanup(func() { _ = ln.Close() }) //nolint:errcheck
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}
