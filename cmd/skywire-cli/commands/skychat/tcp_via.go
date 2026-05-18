// Package cliskychat — tcp_via.go: noise-TCP direct send path for
// `cli skychat send --via tcp://<pk>@host:port`. Mirrors the
// dmsgpty `--via tcp://` pattern: dial the remote chat-app's
// --tcp-listen port, run noise XK with the remote's PK pinned,
// write a chat-msg envelope, optionally wait for chat-ack, close.
//
// Distinct from the default send path (which POSTs to the local
// chat-app's /message HTTP endpoint and lets the chat-app pick a
// transport). --via tcp:// bypasses the local chat-app entirely —
// the CLI process IS the dialer. This is the reliability-floor
// transport: works when the local visor is down, when dmsg-disc
// is unreachable, when the chat-app itself is wedged.
//
// Identity resolution (--sk → DMSGCURL_SK → -c skywire.json →
// ephemeral) matches the chat-app's tcp-direct path so operators
// have one mental model for "what identity goes on the wire".
package cliskychat

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire/tcpnoise"
)

const (
	tcpFrameMaxBytes  = 1 << 20 // 1 MiB upper bound on inbound frame size
	tcpAckPollTimeout = 60 * time.Second
)

// chatEnvelope mirrors cmd/apps/skychat/commands/sendack.go's
// shape. Duplicated here rather than imported to keep the CLI
// binary independent of the chat-app's full dependency graph.
// Field tags MUST stay in sync with the chat-app — every shipping
// peer parses this struct on the wire.
type chatEnvelope struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Body string `json:"body,omitempty"`
	Ack  bool   `json:"ack,omitempty"`
}

// sendViaTCP runs the full --via tcp:// path: parse the spec,
// resolve identity, dial, frame-write the chat-msg envelope,
// optionally wait for ack. Returns an AckResponse on success
// (Acked=true with timing); a non-nil error indicates a transport-
// or handshake-layer failure rather than a peer-side ack timeout
// (which is returned as Acked=false).
func sendViaTCP(viaSpec, recipientPKHex, message string, wait time.Duration) (*AckResponse, error) {
	rPK, addr, err := parseViaTCPSpec(viaSpec)
	if err != nil {
		return nil, err
	}
	// Sanity check: --via PK should match --to PK. Confusing if they
	// disagree — the noise handshake pins --via's PK so --to would
	// be ignored anyway. Reject explicitly to spare operators the
	// debug session.
	if recipientPKHex != "" {
		var want cipher.PubKey
		if err := want.Set(recipientPKHex); err == nil && want != rPK {
			return nil, fmt.Errorf("--to %s != --via tcp:// pk %s (the pinned PK in --via wins; use one or the other)", recipientPKHex, rPK)
		}
	}

	localPK, localSK, err := resolveTCPIdentityCLI()
	if err != nil {
		return nil, err
	}

	dialCtx, cancel := context.WithTimeout(context.Background(), tcpnoise.DialTimeout+tcpnoise.HandshakeTimeout)
	defer cancel()
	conn, err := tcpnoise.Dial(dialCtx, addr, localPK, localSK, rPK)
	if err != nil {
		return nil, err
	}
	defer conn.Close() //nolint:errcheck

	id := newCLIEventID()
	env := chatEnvelope{Type: chatTypeMsg, ID: id, Body: message, Ack: wait > 0}
	payload, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("marshal envelope: %w", err)
	}
	start := time.Now()
	if err := writeFrame(conn, payload); err != nil {
		return nil, fmt.Errorf("write frame: %w", err)
	}
	if wait <= 0 {
		// Fire-and-forget — frame is queued, return immediately.
		return nil, nil
	}

	// Wait for matching chat-ack. Read frames until we see one with
	// our id, or the wait deadline elapses, or the conn errors.
	deadline := time.Now().Add(wait)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return &AckResponse{Acked: false, ID: id, Reason: "timeout"}, nil
		}
		if err := conn.SetReadDeadline(time.Now().Add(remaining)); err != nil {
			return nil, fmt.Errorf("set read deadline: %w", err)
		}
		frame, err := readFrame(conn)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) || isTimeout(err) {
				return &AckResponse{Acked: false, ID: id, Reason: "timeout"}, nil
			}
			return nil, fmt.Errorf("read frame: %w", err)
		}
		var inEnv chatEnvelope
		if err := json.Unmarshal(frame, &inEnv); err != nil {
			// Non-envelope frame — could be a normal chat message
			// arriving while we wait. Ignore and keep reading for the
			// ack.
			continue
		}
		if inEnv.Type == chatTypeAck && inEnv.ID == id {
			return &AckResponse{Acked: true, ID: id, MS: time.Since(start).Milliseconds()}, nil
		}
		// Other envelope type or wrong id — keep waiting.
	}
}

// chatTypeMsg / chatTypeAck mirror the chat-app's constants;
// duplicated for the same reason chatEnvelope is duplicated.
const (
	chatTypeMsg = "chat-msg"
	chatTypeAck = "chat-ack"
)

// parseViaTCPSpec turns "tcp://<66-hex-pk>@host:port" into
// (pk, "host:port"). Same shape as dmsgpty --via tcp.
func parseViaTCPSpec(spec string) (cipher.PubKey, string, error) {
	const prefix = "tcp://"
	if len(spec) < len(prefix) || spec[:len(prefix)] != prefix {
		return cipher.PubKey{}, "", fmt.Errorf("--via tcp: must start with %q", prefix)
	}
	rest := spec[len(prefix):]
	at := -1
	for i := 0; i < len(rest); i++ {
		if rest[i] == '@' {
			at = i
			break
		}
	}
	if at <= 0 || at >= len(rest)-1 {
		return cipher.PubKey{}, "", fmt.Errorf("--via tcp: missing @ separator in %q", spec)
	}
	var pk cipher.PubKey
	if err := pk.Set(rest[:at]); err != nil {
		return cipher.PubKey{}, "", fmt.Errorf("--via tcp: invalid pk: %w", err)
	}
	return pk, rest[at+1:], nil
}

// resolveTCPIdentityCLI mirrors the chat-app's resolveTCPIdentity
// chain (tcp_direct.go): --sk → DMSGCURL_SK → -c config → error.
// CLI binary doesn't have access to the chat-app's globals so we
// duplicate the logic; the resolution ORDER is shared by convention
// and documented in #2706.
func resolveTCPIdentityCLI() (cipher.PubKey, cipher.SecKey, error) {
	var zero cipher.SecKey
	if tcpViaSK != "" {
		var sk cipher.SecKey
		if err := sk.Set(tcpViaSK); err != nil {
			return cipher.PubKey{}, zero, fmt.Errorf("--sk: %w", err)
		}
		pk, err := sk.PubKey()
		if err != nil {
			return cipher.PubKey{}, zero, fmt.Errorf("--sk: derive pk: %w", err)
		}
		return pk, sk, nil
	}
	if env := os.Getenv("DMSGCURL_SK"); env != "" {
		var sk cipher.SecKey
		if err := sk.Set(env); err == nil {
			pk, err := sk.PubKey()
			if err == nil {
				return pk, sk, nil
			}
		}
	}
	if tcpViaConfig != "" {
		data, err := os.ReadFile(tcpViaConfig) //nolint:gosec
		if err != nil {
			return cipher.PubKey{}, zero, fmt.Errorf("-c %s: %w", tcpViaConfig, err)
		}
		var conf struct {
			SK string `json:"sk"`
		}
		if err := json.Unmarshal(data, &conf); err != nil {
			return cipher.PubKey{}, zero, fmt.Errorf("-c %s parse: %w", tcpViaConfig, err)
		}
		if conf.SK == "" {
			return cipher.PubKey{}, zero, fmt.Errorf("-c %s: no sk field", tcpViaConfig)
		}
		var sk cipher.SecKey
		if err := sk.Set(conf.SK); err != nil {
			return cipher.PubKey{}, zero, fmt.Errorf("-c %s sk: %w", tcpViaConfig, err)
		}
		pk, err := sk.PubKey()
		if err != nil {
			return cipher.PubKey{}, zero, fmt.Errorf("-c %s derive pk: %w", tcpViaConfig, err)
		}
		return pk, sk, nil
	}
	return cipher.PubKey{}, zero, errors.New(
		"--via tcp requires identity: set --sk, DMSGCURL_SK env, or -c skywire.json")
}

// writeFrame writes a length-prefixed payload — same wire format
// the chat-app's framedConn uses (4-byte big-endian length + bytes).
func writeFrame(w io.Writer, payload []byte) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload))) //nolint:gosec
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return nil
}

// readFrame reads one length-prefixed payload. Caps frame size to
// tcpFrameMaxBytes so a malicious or buggy peer can't OOM the CLI
// with a giant length header.
func readFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > tcpFrameMaxBytes {
		return nil, fmt.Errorf("frame size %d exceeds %d", n, tcpFrameMaxBytes)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// isTimeout matches the os.ErrDeadlineExceeded shape that
// net.Conn.SetReadDeadline produces on expiry — used to map the
// timeout case onto AckResponse{Acked: false} rather than a hard
// transport error.
func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	type timeouter interface{ Timeout() bool }
	if t, ok := err.(timeouter); ok {
		return t.Timeout()
	}
	return false
}

// newCLIEventID generates an ack-id for chat-msg envelopes. The
// chat-app uses a 16-hex-char id; this matches that shape so the
// debug surfaces (id in --verbose output, id in receiver SSE)
// look uniform regardless of which side sourced the message.
func newCLIEventID() string {
	var buf [8]byte
	now := time.Now().UnixNano()
	binary.BigEndian.PutUint64(buf[:], uint64(now)) //nolint:gosec
	return fmt.Sprintf("%016x", buf)
}
