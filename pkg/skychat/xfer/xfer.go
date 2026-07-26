// Package xfer pkg/skychat/xfer/xfer.go c4-app-chat
//
// xfer is skychat's file-transfer primitive: it moves one file over a single
// skywire stream (a dmsg stream or a skynet route — the caller supplies the
// DialFunc/listener, exactly like pkg/skychat/call). Files are conversation
// messages, Telegram-style: the Offer is announced into chat/group history and
// the bytes stream out-of-band on skyenv.SkychatFilePort so a large transfer
// never blocks the chat-message conn.
//
// Wire protocol on one transfer stream (all frames are pkg/skychat/message
// length-prefixed frames; the DATA between them is raw):
//
//  1. Offer   frame  (sender → receiver: id, name, size, mime, from, group)
//  2. Accept  frame  (receiver → sender: ok, err) — the receiver's AcceptFunc
//     decides here, BEFORE any bytes move, so a declining or
//     over-quota peer never receives the body
//  3. exactly offer.Size raw bytes            (only when Accept.ok)
//  4. Trailer frame  (sender → receiver: sha256 of the streamed bytes)
//  5. Receipt frame  (receiver → sender: id, ok, err) — final verify verdict
//
// The digest is a trailer, not part of the offer, so the sender streams in a
// single pass (works for non-seekable sources such as stdin); the receiver hashes
// what it reads and compares against the trailer before accepting the file as
// complete. All bytes ride the encrypted skywire transport — nothing touches the
// clearnet.
package xfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

// Offer describes a file being sent. It is the first frame on a transfer stream
// and doubles as the metadata rendered as a message in the conversation.
type Offer struct {
	ID    string        `json:"id"`              // unique per transfer
	Name  string        `json:"name"`            // suggested basename (no path)
	Size  int64         `json:"size"`            // byte count
	MIME  string        `json:"mime,omitempty"`  // best-effort content type
	From  cipher.PubKey `json:"from"`            // sender PK
	Group string        `json:"group,omitempty"` // group id when shared to a group (else 1:1)
}

// acceptMsg is the receiver's accept/decline verdict, sent before the body so a
// declined transfer costs nothing but the offer round-trip.
type acceptMsg struct {
	OK  bool   `json:"ok"`
	Err string `json:"err,omitempty"`
}

// trailer carries the digest the sender computed over the streamed bytes.
type trailer struct {
	SHA256 string `json:"sha256"`
}

// Receipt is the receiver's verdict, returned to the sender at the end.
type Receipt struct {
	ID  string `json:"id"`
	OK  bool   `json:"ok"`
	Err string `json:"err,omitempty"`
}

// DialFunc opens a transfer stream to peer:port over a skywire network (a dmsg
// stream or a skynet route). Same shape and contract as voice.DialFunc — the
// manager supplies one that tries whichever network is up, always on the SAME
// port (skyenv.SkychatFilePort) across dmsg and skynet.
type DialFunc func(ctx context.Context, peer cipher.PubKey, port uint16) (net.Conn, error)

// AcceptFunc decides an inbound transfer. It receives the peer that opened the
// stream and the Offer, and returns the destination to stream into plus whether
// to accept. The app implements "auto-accept when already paired 1:1 or sharing a
// group" here, and points dst at a file under the downloads dir. Returning ok
// false declines the transfer (the sender gets a Receipt with OK=false).
type AcceptFunc func(from cipher.PubKey, offer Offer) (dst io.WriteCloser, ok bool)

// MaxOfferSize caps an Offer/Trailer/Receipt control frame. The DATA is not
// bounded by this — only offer.Size bounds it, and the AcceptFunc is free to
// reject an over-large Size before a single byte is read.
const MaxOfferSize = message.MaxFrameSize

const (
	// IdleWindow bounds how long a transfer may make NO progress before it is
	// abandoned. It is deliberately an idle timeout and not a total one: a
	// total deadline is a file-size limit in disguise (a 35 MB file over a
	// slow route legitimately takes minutes), whereas a dead peer still fails
	// within one window.
	IdleWindow = 60 * time.Second

	// drainWindow bounds how long the receiver waits for the sender to hang up
	// after the receipt is written. Short: the sender closes as soon as it has
	// read the receipt, so this only ever absorbs the round-trip. See
	// waitPeerClose.
	drainWindow = 2 * time.Second

	// copyChunk is the body copy's granularity — every chunk re-arms the idle
	// deadline and re-checks ctx.
	copyChunk = 32 << 10
)

var (
	errDeclined = errors.New("xfer: transfer declined")

	// ErrNoReceipt reports that the body AND its digest trailer were written in
	// full but the receiver's verdict never came back. The bytes are on the
	// peer's side — only the confirmation is missing — so a caller should treat
	// this as delivered-but-unconfirmed rather than as a failed transfer.
	ErrNoReceipt = errors.New("xfer: no receipt")
)

// idleConn re-arms the conn's deadline before every read and write, so a
// transfer is bounded by stalling rather than by its total duration. A conn
// without deadline support just ignores the calls.
type idleConn struct {
	net.Conn
	idle time.Duration
}

func (c idleConn) Read(p []byte) (int, error) {
	_ = c.Conn.SetReadDeadline(time.Now().Add(c.idle)) //nolint:errcheck
	return c.Conn.Read(p)
}

func (c idleConn) Write(p []byte) (int, error) {
	_ = c.Conn.SetWriteDeadline(time.Now().Add(c.idle)) //nolint:errcheck
	return c.Conn.Write(p)
}

// progressConn wraps conn so each I/O gets a fresh idle deadline. Any deadline
// already on the conn from a caller is superseded.
func progressConn(conn net.Conn) net.Conn {
	if conn == nil {
		return nil
	}
	return idleConn{Conn: conn, idle: IdleWindow}
}

// copyN moves exactly n bytes, checking ctx between chunks so a cancelled
// transfer stops promptly instead of running to completion. Returns the number
// of bytes copied.
func copyN(ctx context.Context, dst io.Writer, src io.Reader, n int64) (int64, error) {
	buf := make([]byte, copyChunk)
	var done int64
	for done < n {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		want := min(n-done, int64(len(buf)))
		rn, rerr := io.ReadFull(src, buf[:want])
		if rn > 0 {
			wn, werr := dst.Write(buf[:rn])
			done += int64(wn)
			if werr != nil {
				return done, werr
			}
			if wn != rn {
				return done, io.ErrShortWrite
			}
		}
		if rerr != nil {
			if rerr == io.ErrUnexpectedEOF {
				rerr = io.EOF // a short source reads as a truncated stream
			}
			return done, rerr
		}
	}
	return done, nil
}

// waitPeerClose blocks until the peer hangs up (or window elapses), discarding
// anything it sends. The receiver calls this AFTER writing its receipt so the
// sender is the side that closes first: a skywire route group surfaces a remote
// close in the same select as buffered data, so closing here can make the
// sender's pending receipt read return EOF instead of the receipt — reporting a
// delivered file as failed.
func waitPeerClose(conn net.Conn, window time.Duration) {
	if conn == nil {
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(window)) //nolint:errcheck
	var b [1]byte
	_, _ = conn.Read(b[:]) //nolint:errcheck // EOF is the expected outcome
}

// Send streams offer + the first offer.Size bytes of r over conn, then reads the
// receiver's Receipt. The digest trailer is computed over exactly the bytes
// streamed, so r need not be seekable. conn is NOT closed here — the caller owns
// it (it may be a reused/pooled stream).
func Send(ctx context.Context, conn net.Conn, offer Offer, r io.Reader) (Receipt, error) {
	conn = progressConn(conn)

	hdr, err := json.Marshal(offer)
	if err != nil {
		return Receipt{}, fmt.Errorf("xfer: marshal offer: %w", err)
	}
	if err := message.WriteFrame(conn, hdr); err != nil {
		return Receipt{}, fmt.Errorf("xfer: write offer: %w", err)
	}

	// Wait for the receiver's accept/decline before streaming a single byte.
	af, err := message.ReadFrame(conn)
	if err != nil {
		return Receipt{}, fmt.Errorf("xfer: read accept: %w", err)
	}
	var am acceptMsg
	if err := json.Unmarshal(af, &am); err != nil {
		return Receipt{}, fmt.Errorf("xfer: decode accept: %w", err)
	}
	if !am.OK {
		// Declined before the body — a normal outcome, not a transport error.
		return Receipt{ID: offer.ID, OK: false, Err: declineReason(am.Err)}, nil
	}

	h := sha256.New()
	if offer.Size > 0 {
		// Copy to the wire and the hash in one pass.
		n, cerr := copyN(ctx, io.MultiWriter(conn, h), r, offer.Size)
		if cerr != nil {
			return Receipt{}, fmt.Errorf("xfer: stream body (%d/%d bytes): %w", n, offer.Size, cerr)
		}
	}

	tr, err := json.Marshal(trailer{SHA256: hex.EncodeToString(h.Sum(nil))})
	if err != nil {
		return Receipt{}, fmt.Errorf("xfer: marshal trailer: %w", err)
	}
	if err := message.WriteFrame(conn, tr); err != nil {
		return Receipt{}, fmt.Errorf("xfer: write trailer: %w", err)
	}

	rf, err := message.ReadFrame(conn)
	if err != nil {
		// Everything the receiver needs is already on its side; only the verdict
		// is missing. Distinguished so the caller doesn't report a delivered
		// file as failed.
		return Receipt{ID: offer.ID}, fmt.Errorf("%w (read: %v)", ErrNoReceipt, err)
	}
	var rc Receipt
	if err := json.Unmarshal(rf, &rc); err != nil {
		return Receipt{}, fmt.Errorf("xfer: decode receipt: %w", err)
	}
	return rc, nil
}

// Receive reads an Offer from conn, asks accept where to put it (and whether to
// take it at all), streams offer.Size bytes into the destination while hashing,
// verifies the digest against the sender's trailer, and returns a Receipt to the
// sender. It returns the Offer and a nil error on a verified, accepted transfer;
// errDeclined when accept says no; a non-nil error on any wire/verify failure
// (the receiver still tries to send a negative Receipt so the sender learns).
// conn is NOT closed here.
func Receive(ctx context.Context, from cipher.PubKey, conn net.Conn, accept AcceptFunc) (Offer, error) {
	raw := conn
	conn = progressConn(conn)

	of, err := message.ReadFrame(conn)
	if err != nil {
		return Offer{}, fmt.Errorf("xfer: read offer: %w", err)
	}
	var offer Offer
	if err := json.Unmarshal(of, &offer); err != nil {
		return Offer{}, fmt.Errorf("xfer: decode offer: %w", err)
	}
	if offer.Size < 0 {
		_ = sendAccept(conn, false, "negative size") //nolint:errcheck
		return offer, fmt.Errorf("xfer: negative offer size %d", offer.Size)
	}

	dst, ok := accept(from, offer)
	if !ok {
		_ = sendAccept(conn, false, "declined") //nolint:errcheck
		return offer, errDeclined
	}
	if err := sendAccept(conn, true, ""); err != nil {
		_ = dst.Close() //nolint:errcheck
		return offer, fmt.Errorf("xfer: send accept: %w", err)
	}

	h := sha256.New()
	if cerr := streamBody(ctx, conn, dst, h, offer.Size); cerr != nil {
		_ = dst.Close()                                      //nolint:errcheck
		_ = sendReceipt(conn, offer.ID, false, cerr.Error()) //nolint:errcheck
		return offer, cerr
	}
	if cerr := dst.Close(); cerr != nil {
		_ = sendReceipt(conn, offer.ID, false, cerr.Error()) //nolint:errcheck
		return offer, fmt.Errorf("xfer: close destination: %w", cerr)
	}

	tf, err := message.ReadFrame(conn)
	if err != nil {
		_ = sendReceipt(conn, offer.ID, false, "no trailer") //nolint:errcheck
		return offer, fmt.Errorf("xfer: read trailer: %w", err)
	}
	var tr trailer
	if err := json.Unmarshal(tf, &tr); err != nil {
		_ = sendReceipt(conn, offer.ID, false, "bad trailer") //nolint:errcheck
		return offer, fmt.Errorf("xfer: decode trailer: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != tr.SHA256 {
		_ = sendReceipt(conn, offer.ID, false, "checksum mismatch") //nolint:errcheck
		return offer, fmt.Errorf("xfer: checksum mismatch: got %s want %s", got, tr.SHA256)
	}

	if err := sendReceipt(conn, offer.ID, true, ""); err != nil {
		return offer, fmt.Errorf("xfer: send receipt: %w", err)
	}
	// Hand the close over to the sender: it reads the receipt and hangs up,
	// which this read observes. Closing from here first can cost the sender the
	// receipt (see waitPeerClose). Uses the unwrapped conn so the drain window
	// applies rather than the idle window.
	waitPeerClose(raw, drainWindow)
	return offer, nil
}

// streamBody copies exactly size bytes from conn into dst (and h), guarding
// against a short stream.
func streamBody(ctx context.Context, conn net.Conn, dst io.Writer, h hash.Hash, size int64) error {
	if size == 0 {
		return nil
	}
	n, err := copyN(ctx, io.MultiWriter(dst, h), conn, size)
	if err != nil {
		return fmt.Errorf("xfer: stream body (%d/%d bytes): %w", n, size, err)
	}
	return nil
}

func sendAccept(conn net.Conn, ok bool, errMsg string) error {
	b, err := json.Marshal(acceptMsg{OK: ok, Err: errMsg})
	if err != nil {
		return err
	}
	return message.WriteFrame(conn, b)
}

func declineReason(s string) string {
	if s == "" {
		return "declined"
	}
	return s
}

func sendReceipt(conn net.Conn, id string, ok bool, errMsg string) error {
	b, err := json.Marshal(Receipt{ID: id, OK: ok, Err: errMsg})
	if err != nil {
		return err
	}
	return message.WriteFrame(conn, b)
}

// IsDeclined reports whether err is the sentinel returned by Receive when the
// AcceptFunc declined the transfer (so callers can treat it as non-fatal).
func IsDeclined(err error) bool { return errors.Is(err, errDeclined) }

// IsNoReceipt reports whether err means "fully delivered, verdict unknown" —
// the body and its digest went out in full but the receiver's receipt never
// came back. Callers should treat this as sent, not failed.
func IsNoReceipt(err error) bool { return errors.Is(err, ErrNoReceipt) }
