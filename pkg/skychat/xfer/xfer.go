// Package xfer pkg/skychat/xfer/xfer.go c2-app-chat
//
// xfer is skychat's file-transfer primitive: it moves one file over a single
// skywire stream (a dmsg stream or a skynet route — the caller supplies the
// DialFunc/listener, exactly like pkg/skychat/voice). Files are conversation
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

var errDeclined = errors.New("xfer: transfer declined")

// deadlineConn applies ctx's deadline to conn for the whole exchange (best
// effort; a conn without deadline support just ignores it).
func applyDeadline(ctx context.Context, conn net.Conn) {
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl) //nolint:errcheck
	}
}

// Send streams offer + the first offer.Size bytes of r over conn, then reads the
// receiver's Receipt. The digest trailer is computed over exactly the bytes
// streamed, so r need not be seekable. conn is NOT closed here — the caller owns
// it (it may be a reused/pooled stream).
func Send(ctx context.Context, conn net.Conn, offer Offer, r io.Reader) (Receipt, error) {
	applyDeadline(ctx, conn)

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
		n, cerr := io.CopyN(io.MultiWriter(conn, h), r, offer.Size)
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
		return Receipt{}, fmt.Errorf("xfer: read receipt: %w", err)
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
	applyDeadline(ctx, conn)

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
	if cerr := streamBody(conn, dst, h, offer.Size); cerr != nil {
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
	return offer, nil
}

// streamBody copies exactly size bytes from conn into dst (and h), guarding
// against a short stream.
func streamBody(conn net.Conn, dst io.Writer, h hash.Hash, size int64) error {
	if size == 0 {
		return nil
	}
	n, err := io.CopyN(io.MultiWriter(dst, h), conn, size)
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
