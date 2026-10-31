// Package dmsg pkg/dmsg/dmsg/async.go c1-net-dmsg
package dmsg

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// dmsg-async lets a client leave a small message on a dmsg server for a
// client that is offline, and the recipient fetch it later. It is off on
// every server unless the operator turns it on, and each server that has
// it on says so, with its limits, in its discovery entry. docs/dmsg-async.md
// has the design. In short:
//
//   - A client opens a stream to the server itself with a signed
//     StreamRequest whose Async flag is set. The session's Noise channel
//     already encrypts and authenticates it, so the server knows who is
//     asking without anything more.
//   - On that stream it sends one operation as length-prefixed frames:
//     deposit (leave a payload for a PK and port), or fetch (take what
//     waits for the asking PK) followed by ack (delete what was taken).
//   - Which servers hold a recipient's messages is decided by rendezvous
//     hashing over the async servers in discovery, so a sender and a
//     recipient pick the same ones without talking to each other.
//
// Payloads are opaque to the server. A sender that needs secrecy seals
// the payload for its recipient first, as skymail does.

// Async defaults, used for a zero field in AsyncStoreConfig.
const (
	AsyncDefaultMaxMessage        = 64 << 10
	AsyncDefaultTTL               = 72 * time.Hour
	AsyncDefaultPerRecipient      = 100
	AsyncDefaultRecipientBytes    = 1 << 20
	AsyncDefaultSenderPerHour     = 60
	AsyncDefaultMaxTotal          = 64 << 20
	AsyncDefaultReplicas          = 2
	AsyncDefaultFetchInterval     = 10 * time.Minute
	asyncControlMax               = 64 << 10
	asyncFrameTimeout             = 30 * time.Second
	asyncFetchBatchCount          = 32
	asyncFetchBatchBytes          = 1 << 20
	asyncOpDeposit                = "deposit"
	asyncOpFetch                  = "fetch"
	asyncOpAck                    = "ack"
	asyncCodeDisabled             = "disabled"
	asyncCodeTooBig               = "too_big"
	asyncCodeRecipientFull        = "recipient_full"
	asyncCodeSenderLimit          = "sender_limit"
	asyncCodeStoreFull            = "store_full"
	asyncCodeBadRequest           = "bad_request"
	asyncCodeRelayed              = "relayed"
	asyncCodeInternal             = "internal"
	asyncPayloadReadTimeoutFactor = 4
)

// Errors a deposit or fetch can end with. The server's reason travels as a
// code and comes back as one of these.
var (
	ErrAsyncDisabled      = errors.New("dmsg-async: server does not hold messages")
	ErrAsyncTooBig        = errors.New("dmsg-async: message is larger than the server takes")
	ErrAsyncRecipientFull = errors.New("dmsg-async: the recipient has too much waiting on this server")
	ErrAsyncSenderLimit   = errors.New("dmsg-async: this sender has left too many messages lately")
	ErrAsyncStoreFull     = errors.New("dmsg-async: server store is full")
	ErrAsyncBadRequest    = errors.New("dmsg-async: malformed request")
	ErrAsyncRelayed       = errors.New("dmsg-async: only a client's own session may use it")
	ErrAsyncInternal      = errors.New("dmsg-async: server error")
	ErrAsyncNoServers     = errors.New("dmsg-async: no dmsg server in discovery holds messages")
)

var asyncCodeErr = map[string]error{
	asyncCodeDisabled:      ErrAsyncDisabled,
	asyncCodeTooBig:        ErrAsyncTooBig,
	asyncCodeRecipientFull: ErrAsyncRecipientFull,
	asyncCodeSenderLimit:   ErrAsyncSenderLimit,
	asyncCodeStoreFull:     ErrAsyncStoreFull,
	asyncCodeBadRequest:    ErrAsyncBadRequest,
	asyncCodeRelayed:       ErrAsyncRelayed,
	asyncCodeInternal:      ErrAsyncInternal,
}

func asyncErrCode(err error) string {
	for code, e := range asyncCodeErr {
		if errors.Is(err, e) {
			return code
		}
	}
	return asyncCodeInternal
}

// AsyncHeader describes one stored message.
type AsyncHeader struct {
	ID   string        `json:"id"`
	From cipher.PubKey `json:"from"`
	Port uint16        `json:"port"`
	Time time.Time     `json:"time"`
	Size int           `json:"size"`
}

// AsyncMessage is a message fetched from a server.
type AsyncMessage struct {
	AsyncHeader
	Payload []byte
	// Server is the dmsg server that held it.
	Server cipher.PubKey
}

// asyncOp is what a client asks for on an async stream.
type asyncOp struct {
	Op   string        `json:"op"`
	To   cipher.PubKey `json:"to,omitempty"`
	Port uint16        `json:"port,omitempty"`
	Size int           `json:"size,omitempty"`
	IDs  []string      `json:"ids,omitempty"`
	// Ports limits a fetch to messages for these ports, the ones the
	// client has a handler for, so messages for a port it does not serve
	// never stand in front of the rest.
	Ports []uint16 `json:"ports,omitempty"`
}

// asyncReply is the server's answer to an asyncOp.
type asyncReply struct {
	OK       bool          `json:"ok"`
	Code     string        `json:"code,omitempty"`
	Error    string        `json:"error,omitempty"`
	ID       string        `json:"id,omitempty"`
	Messages []AsyncHeader `json:"messages,omitempty"`
	More     bool          `json:"more,omitempty"`
	Deleted  int           `json:"deleted,omitempty"`
}

func (r asyncReply) err() error {
	if r.OK {
		return nil
	}
	if e, ok := asyncCodeErr[r.Code]; ok {
		return e
	}
	return fmt.Errorf("%w: %s", ErrAsyncInternal, r.Error)
}

// writeAsyncFrame writes a 4-byte big-endian length and b.
func writeAsyncFrame(w io.Writer, b []byte) error {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(b))) //nolint:gosec // bounded by the callers' limits
	if _, err := w.Write(n[:]); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}

// readAsyncFrame reads a frame of at most max bytes.
func readAsyncFrame(r io.Reader, max int) ([]byte, error) {
	var n [4]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(n[:])
	if int64(size) > int64(max) {
		return nil, fmt.Errorf("%w: frame of %d bytes over %d", ErrAsyncBadRequest, size, max)
	}
	b := make([]byte, size)
	_, err := io.ReadFull(r, b)
	return b, err
}

func writeAsyncJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeAsyncFrame(w, b)
}

func readAsyncJSON(r io.Reader, v any) error {
	b, err := readAsyncFrame(r, asyncControlMax)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%w: %v", ErrAsyncBadRequest, err)
	}
	return nil
}

// AsyncServersFor picks the k async servers that hold messages for dst,
// by rendezvous hashing (Thaler and Ravishankar, "A Name-Based Mapping
// Scheme for Rendezvous", 1996): each server scores SHA-256(server PK |
// dst PK) and the highest scores win. Everyone with the same server list
// picks the same servers, and a server joining or leaving moves only the
// recipients it wins or held.
func AsyncServersFor(entries []*disc.Entry, dst cipher.PubKey, k int) []*disc.Entry {
	type scored struct {
		e     *disc.Entry
		score uint64
	}
	var all []scored
	seen := map[cipher.PubKey]bool{}
	for _, e := range entries {
		if e == nil || e.Server == nil || e.Server.Async == nil || seen[e.Static] {
			continue
		}
		seen[e.Static] = true
		h := sha256.Sum256(append(append([]byte{}, e.Static[:]...), dst[:]...))
		all = append(all, scored{e: e, score: binary.BigEndian.Uint64(h[:8])})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].e.Static.Hex() < all[j].e.Static.Hex()
	})
	if k > len(all) {
		k = len(all)
	}
	out := make([]*disc.Entry, 0, k)
	for _, s := range all[:k] {
		out = append(out, s.e)
	}
	return out
}
