// Package skymailbridge is the protocol core for the skywire email
// bridge: a minimal SMTP server-side state machine that parses
// inbound envelopes, extracts a peer PubKey from the recipient
// domain's pre-suffix DNS label, dials the peer via a transport-
// agnostic Dialer, and relays the envelope using net/smtp.
//
// Two callers exist:
//
//   - cmd/apps/skymail-bridge — registered visor app. Implements
//     Dialer via app.Client.Dial over skywire's routing layer
//     (TypeSkynet). Reuses the visor's running dmsg session.
//
//   - cmd/smb — standalone binary. Implements Dialer
//     via dmsg.Client.Dial directly, with its own keypair. Useful
//     for headless deployments that don't run a full visor.
//
// Both binaries use exactly the same SMTP server loop, recipient
// parser, and relay logic; only the Dialer differs. The address
// shape supported is documented under ParseRecipient.
package skymailbridge

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
)

const (
	// DefaultSuffix is the TLD the bridge treats as a skywire-routed
	// recipient. Matches the resolver's pkg/skynetweb default so a
	// single mental model covers HTTP, TLS-MITM, and SMTP over skywire.
	DefaultSuffix = ".skynet"

	// DefaultHeloName is the EHLO/HELO greeting the bridge sends to
	// the peer's Postfix. Operators can override; the value mostly
	// shows up in Received: trace headers on the receiver side.
	DefaultHeloName = "skymail-bridge.local"

	// readLineLimit caps a single SMTP command line — RFC 5321
	// §4.5.3.1.4 mandates servers accept 1000-octet text lines, plus
	// CRLF + slop = 1024 is safe.
	readLineLimit = 1024

	// dataSizeLimit caps the DATA payload to keep a runaway sender
	// from exhausting the bridge. 50 MiB matches typical MTA defaults.
	dataSizeLimit = 50 * 1024 * 1024

	// dialTimeout bounds how long we wait for a peer dial. The
	// underlying transport's retry is best-effort; the ceiling keeps
	// a stuck route from holding the inbound SMTP session open.
	dialTimeout = 30 * time.Second
)

// Dialer abstracts the peer-dial step so the same SMTP server loop
// can sit on top of either a skywire-routing transport (visor app
// flavor) or a direct dmsg.Client (standalone flavor).
//
// Implementations must return a usable net.Conn or an error;
// returning (nil, nil) is a contract violation.
type Dialer interface {
	Dial(ctx context.Context, peer cipher.PubKey, port uint16) (net.Conn, error)
}

// Config holds the runtime knobs of a bridge server. Zero-value
// fields fall through to documented defaults.
type Config struct {
	// Suffix is the TLD the bridge treats as skywire-routed.
	// Defaults to DefaultSuffix when empty.
	Suffix string
	// Mode selects how the recipient is rewritten before relay:
	//   "b" — strip ".<pk><Suffix>" from RCPT TO (default).
	//   "a" — forward RCPT TO verbatim. Receiver's Postfix must
	//          accept "<pk><Suffix>" in mydestination.
	Mode string
	// HeloName is the EHLO/HELO name sent to the peer. Defaults to
	// DefaultHeloName when empty.
	HeloName string
	// RemotePort is the peer routing port to dial. Defaults to 25
	// (matching the SMTP convention) when zero.
	RemotePort uint16
}

func (c Config) withDefaults() Config {
	if c.Suffix == "" {
		c.Suffix = DefaultSuffix
	}
	if !strings.HasPrefix(c.Suffix, ".") {
		c.Suffix = "." + c.Suffix
	}
	if c.Mode == "" {
		c.Mode = "b"
	}
	if c.HeloName == "" {
		c.HeloName = DefaultHeloName
	}
	if c.RemotePort == 0 {
		c.RemotePort = 25
	}
	return c
}

// Validate returns nil iff cfg has a usable Mode. Other fields use
// defaults silently; Mode is gated because a typo is more likely
// than not to mean "I wanted the other mode".
func (c Config) Validate() error {
	if c.Mode != "" && c.Mode != "a" && c.Mode != "b" {
		return fmt.Errorf("skymailbridge: invalid Mode %q (want a or b)", c.Mode)
	}
	return nil
}

// Serve runs the bridge until ctx is canceled or lis is closed.
// Each accepted connection gets a dedicated goroutine that walks
// one or more SMTP envelopes; lis.Close() is the canonical shutdown
// signal.
func Serve(ctx context.Context, lis net.Listener, dialer Dialer, cfg Config, log logrus.FieldLogger) error {
	if dialer == nil {
		return errors.New("skymailbridge: Dialer is nil")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	cfg = cfg.withDefaults()
	if log == nil {
		log = logrus.NewEntry(logrus.New())
	}

	var wg sync.WaitGroup
	go func() {
		<-ctx.Done()
		_ = lis.Close() //nolint:errcheck,gosec
	}()

	for {
		c, err := lis.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				break
			}
			log.WithError(err).Warn("skymailbridge: accept")
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer c.Close() //nolint:errcheck,gosec
			handleSession(ctx, c, dialer, cfg, log)
		}()
	}
	wg.Wait()
	return nil
}

// Recipient is one parsed RCPT TO entry.
type Recipient struct {
	Original string        // wire-format as accepted by the bridge
	Forward  string        // address forwarded to the peer, per Mode
	PeerPK   cipher.PubKey // skywire dial target
}

// ParseRecipient inspects an envelope recipient and returns
// (peerPK, forward, isSkynet, err). When the address doesn't end in
// the configured suffix, returns (zero, "", false, nil) — caller
// treats that as a 550 reject rather than an error.
//
// Address layout per mode:
//
//	mode "a":  local@<base32-pk><suffix>
//	mode "b":  local@<host>.<base32-pk><suffix>
//
// Both forms require a single base32-pk DNS label
// (cipher.PubKeyDNSLabelLen chars) immediately before the suffix.
// In mode B the host portion may itself contain dots (e.g.
// "magnetosphere.net.<pk>.skynet").
func ParseRecipient(addr, suffix, mode string) (cipher.PubKey, string, bool, error) {
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return cipher.PubKey{}, "", false, errors.New("missing '@'")
	}
	local, domain := addr[:at], addr[at+1:]
	if !strings.HasSuffix(strings.ToLower(domain), strings.ToLower(suffix)) {
		return cipher.PubKey{}, "", false, nil
	}
	bare := strings.TrimSuffix(strings.ToLower(domain), strings.ToLower(suffix))
	bare = strings.TrimSuffix(bare, ".")
	if bare == "" {
		return cipher.PubKey{}, "", false, errors.New("empty domain before suffix")
	}

	var pkLabel, hostPart string
	if dot := strings.LastIndex(bare, "."); dot >= 0 {
		hostPart = bare[:dot]
		pkLabel = bare[dot+1:]
	} else {
		pkLabel = bare
	}
	if len(pkLabel) != cipher.PubKeyDNSLabelLen {
		return cipher.PubKey{}, "", false, fmt.Errorf("expected %d-char base32 pk label, got %d (%q)",
			cipher.PubKeyDNSLabelLen, len(pkLabel), pkLabel)
	}
	pk, err := cipher.ParseDNSLabel(pkLabel)
	if err != nil {
		return cipher.PubKey{}, "", false, fmt.Errorf("decode pk label: %w", err)
	}

	switch mode {
	case "a":
		return pk, addr, true, nil
	case "b":
		if hostPart == "" {
			return cipher.PubKey{}, "", false, fmt.Errorf("mode b requires <host>.<pk>%s form; got %q", suffix, addr)
		}
		return pk, local + "@" + hostPart, true, nil
	}
	return cipher.PubKey{}, "", false, fmt.Errorf("unknown mode %q", mode)
}

// handleSession runs one SMTP conversation: the bridge plays the
// server role to the local Postfix, then the client role to the
// peer's Postfix once the envelope is complete.
func handleSession(ctx context.Context, c net.Conn, dialer Dialer, cfg Config, log logrus.FieldLogger) {
	br := bufio.NewReaderSize(c, readLineLimit)
	tp := textproto.NewWriter(bufio.NewWriter(c))

	if err := tp.PrintfLine("220 %s skymail-bridge", cfg.HeloName); err != nil {
		return
	}

	var (
		from  string
		rcpts []Recipient
	)
	reset := func() {
		from = ""
		rcpts = nil
	}

	for {
		line, err := readSMTPLine(br)
		if err != nil {
			return
		}
		cmd, arg := splitCommand(line)
		switch strings.ToUpper(cmd) {
		case "HELO":
			_ = tp.PrintfLine("250 %s", cfg.HeloName) //nolint:errcheck,gosec
		case "EHLO":
			_ = tp.PrintfLine("250-%s", cfg.HeloName)       //nolint:errcheck,gosec
			_ = tp.PrintfLine("250-SIZE %d", dataSizeLimit) //nolint:errcheck,gosec
			_ = tp.PrintfLine("250 8BITMIME")               //nolint:errcheck,gosec
		case "MAIL":
			addr, perr := parseAngleAddr(arg, "FROM")
			if perr != nil {
				_ = tp.PrintfLine("501 5.5.4 malformed MAIL FROM: %s", perr) //nolint:errcheck,gosec
				continue
			}
			from = addr
			_ = tp.PrintfLine("250 2.1.0 OK") //nolint:errcheck,gosec
		case "RCPT":
			if from == "" {
				_ = tp.PrintfLine("503 5.5.1 need MAIL before RCPT") //nolint:errcheck,gosec
				continue
			}
			addr, perr := parseAngleAddr(arg, "TO")
			if perr != nil {
				_ = tp.PrintfLine("501 5.5.4 malformed RCPT TO: %s", perr) //nolint:errcheck,gosec
				continue
			}
			pk, forward, isSkynet, perr := ParseRecipient(addr, cfg.Suffix, cfg.Mode)
			if perr != nil {
				_ = tp.PrintfLine("550 5.1.3 %s", perr) //nolint:errcheck,gosec
				continue
			}
			if !isSkynet {
				_ = tp.PrintfLine("550 5.7.1 %s does not end in %s; skymail-bridge only handles skynet recipients", addr, cfg.Suffix) //nolint:errcheck,gosec
				continue
			}
			// All RCPT TOs in one envelope must dial the same peer;
			// per-recipient fanout would require splitting the DATA
			// stream. Reject the extra rcpt with 451 so the upstream
			// Postfix retries it separately.
			if len(rcpts) > 0 && rcpts[0].PeerPK != pk {
				_ = tp.PrintfLine("451 4.7.1 skymail-bridge: envelope mixes peers (%s vs %s); requeue per-recipient", rcpts[0].PeerPK, pk) //nolint:errcheck,gosec
				continue
			}
			rcpts = append(rcpts, Recipient{Original: addr, Forward: forward, PeerPK: pk})
			_ = tp.PrintfLine("250 2.1.5 OK") //nolint:errcheck,gosec
		case "DATA":
			if from == "" || len(rcpts) == 0 {
				_ = tp.PrintfLine("503 5.5.1 need MAIL+RCPT before DATA") //nolint:errcheck,gosec
				continue
			}
			_ = tp.PrintfLine("354 end with <CR><LF>.<CR><LF>") //nolint:errcheck,gosec
			body, derr := readDATA(br)
			if derr != nil {
				_ = tp.PrintfLine("451 4.3.0 read DATA: %s", derr) //nolint:errcheck,gosec
				reset()
				continue
			}
			if rerr := Relay(ctx, dialer, cfg, from, rcpts, body, log); rerr != nil {
				log.WithError(rerr).Warn("skymailbridge: relay")
				_ = tp.PrintfLine("451 4.4.1 peer relay failed: %s", rerr) //nolint:errcheck,gosec
			} else {
				_ = tp.PrintfLine("250 2.0.0 Ok: queued via skymail-bridge") //nolint:errcheck,gosec
			}
			reset()
		case "RSET":
			reset()
			_ = tp.PrintfLine("250 2.0.0 OK") //nolint:errcheck,gosec
		case "NOOP":
			_ = tp.PrintfLine("250 2.0.0 OK") //nolint:errcheck,gosec
		case "QUIT":
			_ = tp.PrintfLine("221 2.0.0 bye") //nolint:errcheck,gosec
			return
		default:
			_ = tp.PrintfLine("502 5.5.2 unknown command %q", cmd) //nolint:errcheck,gosec
		}
	}
}

// Relay dials the peer via dialer and forwards an SMTP envelope.
// Errors are surfaced verbatim so the calling SMTP session can
// translate them to an appropriate 4xx/5xx for the upstream
// Postfix.
func Relay(ctx context.Context, dialer Dialer, cfg Config, from string, rcpts []Recipient, body []byte, log logrus.FieldLogger) error {
	if len(rcpts) == 0 {
		return errors.New("no recipients")
	}
	peer := rcpts[0].PeerPK
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	conn, err := dialer.Dial(dialCtx, peer, cfg.RemotePort)
	if err != nil {
		return fmt.Errorf("dial %s:%d: %w", peer, cfg.RemotePort, err)
	}

	cl, err := smtp.NewClient(conn, peer.Hex())
	if err != nil {
		_ = conn.Close() //nolint:errcheck,gosec
		return fmt.Errorf("smtp.NewClient: %w", err)
	}
	defer func() { _ = cl.Close() }() //nolint:errcheck,gosec

	if err := cl.Hello(cfg.HeloName); err != nil {
		return fmt.Errorf("HELO: %w", err)
	}
	if err := cl.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	for _, r := range rcpts {
		if err := cl.Rcpt(r.Forward); err != nil {
			return fmt.Errorf("RCPT TO %s (was %s): %w", r.Forward, r.Original, err)
		}
	}
	wc, err := cl.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := io.Copy(wc, strings.NewReader(string(body))); err != nil {
		_ = wc.Close() //nolint:errcheck,gosec
		return fmt.Errorf("write DATA: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("close DATA: %w", err)
	}
	if err := cl.Quit(); err != nil {
		log.WithError(err).Debug("QUIT (already-delivered, ignoring)")
	}
	log.WithField("peer", peer.Hex()).WithField("rcpts", len(rcpts)).
		WithField("bytes", len(body)).Info("skymailbridge: relayed envelope")
	return nil
}

// readSMTPLine reads a single CRLF-terminated SMTP command line.
// Lines longer than readLineLimit are an error; truncate-and-
// continue would corrupt parser state.
func readSMTPLine(br *bufio.Reader) (string, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return "", err
	}
	if len(line) > readLineLimit {
		return "", fmt.Errorf("line too long (%d > %d)", len(line), readLineLimit)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// readDATA reads the message body up to the lone-"." terminator,
// undoing dot-stuffing per RFC 5321 §4.5.2.
func readDATA(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if len(buf)+len(line) > dataSizeLimit {
			return nil, fmt.Errorf("DATA exceeds %d octets", dataSizeLimit)
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "." {
			return buf, nil
		}
		// Un-dot-stuff per RFC 5321 §4.5.2: a leading "." that
		// isn't itself the terminator was added by the sender and
		// must be stripped before storage.
		trimmed = strings.TrimPrefix(trimmed, ".")
		buf = append(buf, trimmed...)
		buf = append(buf, '\r', '\n')
	}
}

// splitCommand splits an SMTP command line into verb + remainder.
// Verb is whatever appears before the first ASCII whitespace.
func splitCommand(line string) (verb, rest string) {
	for i := 0; i < len(line); i++ {
		if line[i] == ' ' || line[i] == '\t' {
			return line[:i], strings.TrimLeft(line[i+1:], " \t")
		}
	}
	return line, ""
}

// parseAngleAddr parses MAIL FROM:<addr> / RCPT TO:<addr>. Extra
// ESMTP parameters after the closing '>' are accepted and silently
// discarded; the bridge passes the bare address through.
func parseAngleAddr(arg, keyword string) (string, error) {
	upper := strings.ToUpper(arg)
	prefix := keyword + ":"
	if !strings.HasPrefix(upper, prefix) {
		return "", fmt.Errorf("missing %q", prefix)
	}
	rest := strings.TrimSpace(arg[len(prefix):])
	if !strings.HasPrefix(rest, "<") {
		return "", errors.New("missing '<'")
	}
	end := strings.Index(rest, ">")
	if end < 0 {
		return "", errors.New("missing '>'")
	}
	return rest[1:end], nil
}
