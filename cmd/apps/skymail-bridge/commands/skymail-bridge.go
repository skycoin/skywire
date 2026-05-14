// Package commands cmd/apps/skymail-bridge/commands/skymail-bridge.go
//
// skymail-bridge is the sender-side half of skywire email delivery.
//
// It accepts incoming SMTP from a co-located Postfix instance whose
// transport_map routes the .skynet suffix to the bridge's listener,
// parses each RCPT TO to extract a base32-encoded peer PubKey from
// the recipient domain, dials the peer's visor over the skywire
// routing mesh, and relays the envelope. The peer's visor is
// expected to expose its local Postfix smtpd on the chosen routing
// port via `skywire cli serve add <port> --to 127.0.0.1:25`.
//
// Address shapes supported:
//
//	Mode B (default):  user@<host>.<base32-pk>.skynet
//	                   → RCPT TO rewritten to user@<host> before
//	                     reaching the peer's Postfix. The receiver
//	                     keeps its existing domain identity; no
//	                     mydestination changes required on the
//	                     receiver's Postfix.
//
//	Mode A:            user@<base32-pk>.skynet
//	                   → RCPT TO forwarded verbatim. Receiver's
//	                     Postfix must accept <base32-pk>.skynet in
//	                     its mydestination list.
//
// PubKey labels are RFC 4648 base32 (unpadded, lowercase) per the
// existing pkg/cipher.PubKey.DNSLabel() / ParseDNSLabel: 53 ASCII
// chars, fits in a single 63-octet DNS label (RFC 1035 §2.3.1) and
// thus is acceptable inside an SMTP envelope domain (RFC 5321
// §4.1.2 ties domains to RFC 1035 hostnames).
//
// What this app does NOT do (yet):
//   - STARTTLS to either the local sender or the peer. The bridge
//     is expected to live behind localhost-only Postfix submission.
//   - SMTP AUTH. Auth happens between the user MUA and the local
//     Postfix; the bridge sees post-auth traffic from localhost.
//   - Pipelining beyond the trivial subset. Sender-side Postfix is
//     well-behaved; pipelining is announced but not exercised.
//   - Sender-side spam filtering. The receiver enforces an allowlist
//     via the standard `skywire cli serve whitelist` mechanism.
package commands

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/smtp"
	"net/textproto"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

const (
	netType       = appnet.TypeSkynet
	defaultSuffix = ".skynet"

	// readLineLimit caps a single SMTP command line so a misbehaving
	// peer can't tie up memory; RFC 5321 §4.5.3.1.4 mandates servers
	// accept 1000-octet text lines, plus CRLF + slop = 1024 is safe.
	readLineLimit = 1024

	// dataSizeLimit caps the DATA payload. 50 MiB matches typical
	// MTA defaults and keeps a runaway sender from exhausting the
	// bridge's memory.
	dataSizeLimit = 50 * 1024 * 1024

	// peerDialTimeout bounds how long we wait for the skywire route
	// to the peer. Skywire's own dial path is best-effort already;
	// adding a ceiling keeps a stuck route from holding the inbound
	// SMTP session open forever.
	peerDialTimeout = 30 * time.Second
)

var (
	bindAddr   string
	suffix     string
	mode       string
	heloName   string
	remotePort uint16
	appPort    uint16
)

func init() {
	launcher.RegisterApp(skyenv.SkymailBridgeName, RunSkymailBridge)
	RootCmd.Flags().StringVar(&bindAddr, "addr", skyenv.SkymailBridgeAddr, "local SMTP listen address (Postfix transport_map target)")
	RootCmd.Flags().StringVar(&suffix, "suffix", defaultSuffix, "TLD suffix that routes over skynet")
	RootCmd.Flags().StringVar(&mode, "mode", "b", "envelope mode: a=verbatim RCPT TO, b=strip .<pk><suffix> before forwarding")
	RootCmd.Flags().StringVar(&heloName, "helo", "skymail-bridge.local", "HELO/EHLO name presented to the peer's Postfix")
	RootCmd.Flags().Uint16Var(&remotePort, "remote-port", uint16(skyenv.SmtpPort), "skywire routing port to dial on the peer")
	RootCmd.Flags().Uint16Var(&appPort, "port", 0, "routing port for communication between app and visor (0 = visor-assigned)")
}

// RootCmd is the cobra entry for `skymail-bridge`.
var RootCmd = &cobra.Command{
	Use:                   skyenv.SkymailBridgeName,
	Short:                 "SMTP-aware proxy that relays .skynet recipient envelopes over skywire",
	Long:                  calvin.AsciiFont(skyenv.SkymailBridgeName),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		ctx := context.Background()
		if err := RunSkymailBridge(ctx, args); err != nil {
			log.Fatal(err)
		}
	},
}

// Execute runs RootCmd. Called by the package-main entrypoint.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

// RunSkymailBridge is registered with launcher.RegisterApp so the
// visor's launcher can spawn the app in-process. Re-parses flags
// from args when the visor passes them down.
func RunSkymailBridge(ctx context.Context, args []string) error {
	if len(args) > 0 {
		fs := pflag.NewFlagSet(skyenv.SkymailBridgeName, pflag.ContinueOnError)
		fs.StringVar(&bindAddr, "addr", skyenv.SkymailBridgeAddr, "local SMTP listen address")
		fs.StringVar(&suffix, "suffix", defaultSuffix, "TLD suffix routed over skynet")
		fs.StringVar(&mode, "mode", "b", "envelope mode")
		fs.StringVar(&heloName, "helo", "skymail-bridge.local", "HELO/EHLO name")
		fs.Uint16Var(&remotePort, "remote-port", uint16(skyenv.SmtpPort), "remote routing port")
		fs.Uint16Var(&appPort, "port", 0, "routing port")
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("parse flags: %w", err)
		}
	}
	if mode != "a" && mode != "b" {
		return fmt.Errorf("invalid --mode %q (want a or b)", mode)
	}
	if !strings.HasPrefix(suffix, ".") {
		suffix = "." + suffix
	}

	appCl := app.NewClient(nil)
	defer appCl.Close()
	logger := appCl.Log()

	port := appCl.Config().RoutingPort
	if appPort != 0 {
		port = routing.Port(appPort)
	}
	if err := appCl.SetAppPort(port); err != nil {
		logger.WithError(err).Warn("SetAppPort")
	}

	bi := buildinfo.Get()
	logger.Infof("skymail-bridge %s built on %s (commit %s) — mode=%s suffix=%s",
		bi.Version, bi.Date, bi.Commit, mode, suffix)

	defer setAppStatus(appCl, logger, appserver.AppDetailedStatusStopped)

	lis, err := net.Listen("tcp", bindAddr)
	if err != nil {
		setAppErr(appCl, logger, err)
		return fmt.Errorf("listen %s: %w", bindAddr, err)
	}
	logger.Infof("skymail-bridge accepting SMTP on %s", bindAddr)
	setAppStatus(appCl, logger, appserver.AppDetailedStatusRunning)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Graceful shutdown on SIGINT/SIGTERM mirrors skysocks-client.
	termCh := make(chan os.Signal, 1)
	signal.Notify(termCh, os.Interrupt)
	go func() {
		select {
		case <-termCh:
			_ = lis.Close() //nolint:errcheck,gosec
		case <-ctx.Done():
			_ = lis.Close() //nolint:errcheck,gosec
		}
	}()

	var wg sync.WaitGroup
	for {
		c, aerr := lis.Accept()
		if aerr != nil {
			if errors.Is(aerr, net.ErrClosed) {
				break
			}
			logger.WithError(aerr).Warn("accept")
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer c.Close() //nolint:errcheck,gosec
			handleSession(ctx, c, appCl, logger)
		}()
	}
	wg.Wait()
	return nil
}

// recipient is one parsed RCPT TO target.
type recipient struct {
	original string         // wire-format as accepted by the bridge
	forward  string         // address to use on the outbound side per mode
	peerPK   cipher.PubKey  // skywire dial target
}

// handleSession runs one inbound SMTP conversation. The bridge plays
// the server role to the local Postfix and the client role to the
// peer's Postfix once the envelope is complete.
func handleSession(ctx context.Context, c net.Conn, appCl *app.Client, logger logrus.FieldLogger) {
	br := bufio.NewReaderSize(c, readLineLimit)
	tp := textproto.NewWriter(bufio.NewWriter(c))

	if err := tp.PrintfLine("220 %s skymail-bridge", heloName); err != nil {
		return
	}

	var (
		from  string
		rcpts []recipient
	)
	resetEnvelope := func() {
		from = ""
		rcpts = nil
	}

	for {
		line, err := readLine(br)
		if err != nil {
			return
		}
		cmd, arg := splitCommand(line)
		switch strings.ToUpper(cmd) {
		case "HELO":
			_ = tp.PrintfLine("250 %s", heloName) //nolint:errcheck,gosec
		case "EHLO":
			// 250- multi-line: only one extension worth advertising
			// at this scope (SIZE). PIPELINING is intentionally
			// omitted because the bridge's outbound side does not
			// pipeline.
			_ = tp.PrintfLine("250-%s", heloName)                  //nolint:errcheck,gosec
			_ = tp.PrintfLine("250-SIZE %d", dataSizeLimit)        //nolint:errcheck,gosec
			_ = tp.PrintfLine("250 8BITMIME")                      //nolint:errcheck,gosec
		case "MAIL":
			addr, perr := parseFromArg(arg)
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
			addr, perr := parseToArg(arg)
			if perr != nil {
				_ = tp.PrintfLine("501 5.5.4 malformed RCPT TO: %s", perr) //nolint:errcheck,gosec
				continue
			}
			pk, forward, isSkynet, perr := parseSkynetRecipient(addr, suffix, mode)
			if perr != nil {
				_ = tp.PrintfLine("550 5.1.3 %s", perr) //nolint:errcheck,gosec
				continue
			}
			if !isSkynet {
				_ = tp.PrintfLine("550 5.7.1 %s does not end in %s; skymail-bridge only handles skynet recipients", addr, suffix) //nolint:errcheck,gosec
				continue
			}
			// All RCPT TOs in one envelope must dial the same peer;
			// per-recipient fanout would require splitting the DATA
			// stream which the bridge doesn't do (yet). Reject the
			// extra rcpt with 451 so Postfix retries it separately.
			if len(rcpts) > 0 && rcpts[0].peerPK != pk {
				_ = tp.PrintfLine("451 4.7.1 skymail-bridge: envelope mixes peers (%s vs %s); requeue per-recipient", rcpts[0].peerPK, pk) //nolint:errcheck,gosec
				continue
			}
			rcpts = append(rcpts, recipient{original: addr, forward: forward, peerPK: pk})
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
				resetEnvelope()
				continue
			}
			if relayErr := relayEnvelope(ctx, appCl, logger, from, rcpts, body); relayErr != nil {
				logger.WithError(relayErr).Warn("relay")
				_ = tp.PrintfLine("451 4.4.1 peer relay failed: %s", relayErr) //nolint:errcheck,gosec
			} else {
				_ = tp.PrintfLine("250 2.0.0 Ok: queued via skymail-bridge") //nolint:errcheck,gosec
			}
			resetEnvelope()
		case "RSET":
			resetEnvelope()
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

// readLine reads a single CRLF-terminated SMTP command line up to
// readLineLimit octets. Lines longer than the cap are an error;
// truncating-and-continuing would corrupt the parse state.
func readLine(br *bufio.Reader) (string, error) {
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
		if strings.HasPrefix(trimmed, ".") {
			// Un-dot-stuff: a leading "." was added by the sender per
			// §4.5.2; strip the first one.
			trimmed = trimmed[1:]
		}
		buf = append(buf, trimmed...)
		buf = append(buf, '\r', '\n')
	}
}

// splitCommand splits an SMTP command line into its verb and
// remainder. The verb is whatever appears before the first ASCII
// whitespace; the remainder includes everything after, with leading
// whitespace stripped.
func splitCommand(line string) (verb, rest string) {
	for i := 0; i < len(line); i++ {
		if line[i] == ' ' || line[i] == '\t' {
			return line[:i], strings.TrimLeft(line[i+1:], " \t")
		}
	}
	return line, ""
}

// parseFromArg parses the argument of MAIL FROM:<addr> per RFC 5321
// §4.1.1.2. Extra ESMTP parameters (SIZE= etc.) are accepted and
// discarded — the bridge passes the bare address through.
func parseFromArg(arg string) (string, error) {
	return parseAngleAddr(arg, "FROM")
}

// parseToArg parses the argument of RCPT TO:<addr> per §4.1.1.3.
func parseToArg(arg string) (string, error) {
	return parseAngleAddr(arg, "TO")
}

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

// parseSkynetRecipient inspects an envelope recipient and returns
// (peerPK, forwardAddr, isSkynet, err). If the address doesn't end
// in the configured suffix, returns (zero, "", false, nil) — the
// caller treats that as a 550 reject rather than an error.
//
// Address layout per mode:
//
//	mode "a":  local@<base32-pk><suffix>
//	mode "b":  local@<host>.<base32-pk><suffix>
//
// Both forms expect a single base32-pk label (53 chars) immediately
// before the suffix. The host portion in mode B may contain dots
// (e.g. "magnetosphere.net.<pk>.skynet").
func parseSkynetRecipient(addr, suffix, mode string) (cipher.PubKey, string, bool, error) {
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
		return cipher.PubKey{}, "", false, fmt.Errorf("expected %d-char base32 pk label, got %d (%q)", cipher.PubKeyDNSLabelLen, len(pkLabel), pkLabel)
	}
	pk, err := cipher.ParseDNSLabel(pkLabel)
	if err != nil {
		return cipher.PubKey{}, "", false, fmt.Errorf("decode pk label: %w", err)
	}

	switch mode {
	case "a":
		// Verbatim. Receiver Postfix needs <pk><suffix> in mydestination.
		return pk, addr, true, nil
	case "b":
		if hostPart == "" {
			return cipher.PubKey{}, "", false, fmt.Errorf("mode b requires <host>.<pk>%s form; got %q", suffix, addr)
		}
		return pk, local + "@" + hostPart, true, nil
	}
	return cipher.PubKey{}, "", false, fmt.Errorf("unknown mode %q", mode)
}

// relayEnvelope dials the peer over skywire and forwards the
// envelope via net/smtp. Any error returned causes the inbound
// session to respond 451 so Postfix retries on its own schedule.
func relayEnvelope(ctx context.Context, appCl *app.Client, logger logrus.FieldLogger, from string, rcpts []recipient, body []byte) error {
	if len(rcpts) == 0 {
		return errors.New("no recipients")
	}
	peer := rcpts[0].peerPK
	dialCtx, cancel := context.WithTimeout(ctx, peerDialTimeout)
	defer cancel()

	type dialRes struct {
		c   net.Conn
		err error
	}
	resC := make(chan dialRes, 1)
	go func() {
		c, err := appCl.Dial(appnet.Addr{Net: netType, PubKey: peer, Port: routing.Port(remotePort)})
		resC <- dialRes{c, err}
	}()
	var conn net.Conn
	select {
	case <-dialCtx.Done():
		return fmt.Errorf("dial %s:%d timed out", peer, remotePort)
	case r := <-resC:
		if r.err != nil {
			return fmt.Errorf("dial %s:%d: %w", peer, remotePort, r.err)
		}
		conn = r.c
	}

	cl, err := smtp.NewClient(conn, peer.Hex())
	if err != nil {
		_ = conn.Close() //nolint:errcheck,gosec
		return fmt.Errorf("smtp.NewClient: %w", err)
	}
	defer func() {
		_ = cl.Close() //nolint:errcheck,gosec
	}()

	if err := cl.Hello(heloName); err != nil {
		return fmt.Errorf("HELO: %w", err)
	}
	if err := cl.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	for _, r := range rcpts {
		if err := cl.Rcpt(r.forward); err != nil {
			return fmt.Errorf("RCPT TO %s (was %s): %w", r.forward, r.original, err)
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
		logger.WithError(err).Debug("QUIT (already-delivered, ignoring)")
	}
	logger.WithField("peer", peer.Hex()).WithField("rcpts", len(rcpts)).
		WithField("bytes", len(body)).Info("relayed envelope")
	return nil
}

func setAppErr(appCl *app.Client, logger logrus.FieldLogger, err error) {
	if appErr := appCl.SetError(err.Error()); appErr != nil {
		logger.WithError(appErr).WithField("original_error", err).Warn("Failed to set error")
	}
}

func setAppStatus(appCl *app.Client, logger logrus.FieldLogger, status appserver.AppDetailedStatus) {
	if err := appCl.SetDetailedStatus(string(status)); err != nil {
		logger.WithError(err).WithField("status", status).Warn("Failed to set status")
	}
}
