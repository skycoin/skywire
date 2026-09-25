// Package skymailbridge pkg/skymailbridge/session.go c4-app-mail
package skymailbridge

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// acceptRetryDelay paces the accept loop after a transient error so a
// listener that keeps failing without reporting net.ErrClosed cannot spin.
const acceptRetryDelay = 100 * time.Millisecond

// Envelope is one complete SMTP transaction: the reverse path, every
// accepted forward path, and the un-dot-stuffed message as received.
type Envelope struct {
	Conn  net.Conn // the session's connection; RemoteAddr names the peer
	From  string
	Rcpts []string
	Body  []byte
}

// Handler decides what an SMTP session does with the mail it receives.
// The session loop owns the protocol; a Handler owns policy (Rcpt) and
// delivery (Deliver). The bridge relays to a peer; a mailbox stores.
type Handler interface {
	// Rcpt accepts or rejects one RCPT TO. accepted holds the recipients
	// already taken for this envelope. Return a *Reply to choose the
	// SMTP code; any other error is answered 550 5.1.1.
	Rcpt(c net.Conn, from, rcpt string, accepted []string) error
	// Deliver takes a complete envelope. The returned text follows
	// "250 2.0.0" on success. Return a *Reply to choose the failure
	// code; any other error is answered 451 4.3.0.
	Deliver(ctx context.Context, env Envelope) (string, error)
}

// Reply is an SMTP reply a Handler returns as an error to pick the code
// the session sends.
type Reply struct {
	Code     int
	Enhanced string // RFC 3463 status, e.g. "5.7.1"
	Text     string
}

func (r *Reply) Error() string {
	return fmt.Sprintf("%d %s %s", r.Code, r.Enhanced, r.Text)
}

// replyOr renders err as a *Reply when it is one, else as the fallback.
func replyOr(err error, code int, enhanced string) string {
	var r *Reply
	if errors.As(err, &r) {
		return r.Error()
	}
	return fmt.Sprintf("%d %s %s", code, enhanced, err)
}

func orDefaultLog(log logrus.FieldLogger) logrus.FieldLogger {
	if log == nil {
		return logrus.NewEntry(logrus.New())
	}
	return log
}

// ServeHandler runs an SMTP server on lis until ctx is canceled or lis
// is closed, handing every connection to its own session with h.
func ServeHandler(ctx context.Context, lis net.Listener, h Handler, heloName string, log logrus.FieldLogger) error {
	if h == nil {
		return errors.New("skymailbridge: Handler is nil")
	}
	if heloName == "" {
		heloName = DefaultHeloName
	}
	log = orDefaultLog(log)

	var wg sync.WaitGroup
	go func() {
		<-ctx.Done()
		_ = lis.Close() //nolint:errcheck,gosec
	}()

	for {
		c, err := lis.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				break
			}
			log.WithError(err).Warn("skymailbridge: accept")
			time.Sleep(acceptRetryDelay)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer c.Close() //nolint:errcheck,gosec
			handleSession(ctx, c, h, heloName)
		}()
	}
	wg.Wait()
	return nil
}

// handleSession runs one SMTP conversation, server side.
func handleSession(ctx context.Context, c net.Conn, h Handler, heloName string) {
	br := bufio.NewReaderSize(c, readLineLimit)
	tp := textproto.NewWriter(bufio.NewWriter(c))

	if err := tp.PrintfLine("220 %s ESMTP skymail", heloName); err != nil {
		return
	}

	var (
		from     string
		haveFrom bool
		rcpts    []string
	)
	reset := func() {
		from, haveFrom, rcpts = "", false, nil
	}

	for {
		line, err := readSMTPLine(br)
		if err != nil {
			return
		}
		cmd, arg := splitCommand(line)
		switch strings.ToUpper(cmd) {
		case "HELO":
			_ = tp.PrintfLine("250 %s", heloName) //nolint:errcheck,gosec
		case "EHLO":
			_ = tp.PrintfLine("250-%s", heloName)           //nolint:errcheck,gosec
			_ = tp.PrintfLine("250-SIZE %d", dataSizeLimit) //nolint:errcheck,gosec
			_ = tp.PrintfLine("250 8BITMIME")               //nolint:errcheck,gosec
		case "MAIL":
			addr, perr := parseAngleAddr(arg, "FROM")
			if perr != nil {
				_ = tp.PrintfLine("501 5.5.4 malformed MAIL FROM: %s", perr) //nolint:errcheck,gosec
				continue
			}
			// The null reverse path <> (bounces) is a valid sender.
			reset()
			from, haveFrom = addr, true
			_ = tp.PrintfLine("250 2.1.0 OK") //nolint:errcheck,gosec
		case "RCPT":
			if !haveFrom {
				_ = tp.PrintfLine("503 5.5.1 need MAIL before RCPT") //nolint:errcheck,gosec
				continue
			}
			addr, perr := parseAngleAddr(arg, "TO")
			if perr != nil {
				_ = tp.PrintfLine("501 5.5.4 malformed RCPT TO: %s", perr) //nolint:errcheck,gosec
				continue
			}
			if rerr := h.Rcpt(c, from, addr, rcpts); rerr != nil {
				_ = tp.PrintfLine("%s", replyOr(rerr, 550, "5.1.1")) //nolint:errcheck,gosec
				continue
			}
			rcpts = append(rcpts, addr)
			_ = tp.PrintfLine("250 2.1.5 OK") //nolint:errcheck,gosec
		case "DATA":
			if !haveFrom || len(rcpts) == 0 {
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
			ok, rerr := h.Deliver(ctx, Envelope{Conn: c, From: from, Rcpts: rcpts, Body: body})
			if rerr != nil {
				_ = tp.PrintfLine("%s", replyOr(rerr, 451, "4.3.0")) //nolint:errcheck,gosec
			} else {
				_ = tp.PrintfLine("250 2.0.0 %s", ok) //nolint:errcheck,gosec
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
