// Package dmsgasync pkg/dmsg/dmsgasync/dmsgasync.go c1-net-dmsg
//
// Store and forward for offline dmsg keys. A recipient chooses inbox hosts,
// signs a Record naming them and registers with each. A sender that cannot
// reach the recipient deposits a payload on one of its hosts, and the
// recipient fetches it when it comes back. The host applies the recipient's
// sender Policy to the sender key the dmsg stream authenticates.
// See docs/design/dmsg-async.md.
package dmsgasync

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Defaults for a zero field in Limits.
const (
	DefaultMaxMessage        = 64 << 10
	DefaultMaxPerRecipient   = 200
	DefaultMaxRecipientBytes = 8 << 20
	DefaultMaxPerSender      = 20
	DefaultMaxTotal          = 256 << 20
	DefaultTTL               = 7 * 24 * time.Hour
	// MaxMessageLimit caps any message size, since a fetch reply carries at
	// least one whole message.
	MaxMessageLimit = 1 << 20
	// MaxRecordHosts bounds the hosts one Record may name.
	MaxRecordHosts = 4
	// MaxRecordLifetime bounds how far ahead a Record may expire.
	MaxRecordLifetime = 30 * 24 * time.Hour

	controlMax      = 64 << 10
	fetchBatchCount = 32
	fetchBatchBytes = MaxMessageLimit
	sweepInterval   = 10 * time.Minute
	opTimeout       = 60 * time.Second
	refLen          = 16
)

// Operations a client sends.
const (
	OpRegister   = "register"
	OpUnregister = "unregister"
	OpRecord     = "record"
	OpDeposit    = "deposit"
	OpFetch      = "fetch"
	OpAck        = "ack"
)

// Errors an operation can end with. The host sends a code and the client maps
// it back to one of these.
var (
	ErrNotHosted     = errors.New("dmsg-async: host does not hold messages for this key")
	ErrNotAllowed    = errors.New("dmsg-async: the recipient does not accept messages from this sender")
	ErrTooBig        = errors.New("dmsg-async: message is larger than the host takes")
	ErrRecipientFull = errors.New("dmsg-async: the recipient has too much waiting on this host")
	ErrSenderFull    = errors.New("dmsg-async: this sender has too much waiting for the recipient")
	ErrStoreFull     = errors.New("dmsg-async: host store is full")
	ErrBadRecord     = errors.New("dmsg-async: invalid inbox record")
	ErrBadRequest    = errors.New("dmsg-async: malformed request")
	ErrInternal      = errors.New("dmsg-async: host error")
)

var codeErr = map[string]error{
	"not_hosted":     ErrNotHosted,
	"not_allowed":    ErrNotAllowed,
	"too_big":        ErrTooBig,
	"recipient_full": ErrRecipientFull,
	"sender_full":    ErrSenderFull,
	"store_full":     ErrStoreFull,
	"bad_record":     ErrBadRecord,
	"bad_request":    ErrBadRequest,
	"internal":       ErrInternal,
}

func errCode(err error) string {
	for code, e := range codeErr {
		if errors.Is(err, e) {
			return code
		}
	}
	return "internal"
}

// Header describes one stored message.
type Header struct {
	ID        string        `json:"id"`
	From      cipher.PubKey `json:"from"`
	Port      uint16        `json:"port"`
	Size      int           `json:"size"`
	Deposited time.Time     `json:"deposited"`
}

// Message is a fetched message.
type Message struct {
	Header
	Payload []byte
	// Host is the inbox host that held it.
	Host cipher.PubKey
}

// request is one operation on a stream.
type request struct {
	Op     string        `json:"op"`
	Record *Record       `json:"record,omitempty"`
	Policy *Policy       `json:"policy,omitempty"`
	PK     cipher.PubKey `json:"pk,omitempty"`
	To     cipher.PubKey `json:"to,omitempty"`
	Port   uint16        `json:"port,omitempty"`
	Size   int           `json:"size,omitempty"`
	Ref    string        `json:"ref,omitempty"`
	Ports  []uint16      `json:"ports,omitempty"`
	IDs    []string      `json:"ids,omitempty"`
}

// reply is the host's answer.
type reply struct {
	OK       bool     `json:"ok"`
	Code     string   `json:"code,omitempty"`
	Error    string   `json:"error,omitempty"`
	ID       string   `json:"id,omitempty"`
	Record   *Record  `json:"record,omitempty"`
	Messages []Header `json:"messages,omitempty"`
	More     bool     `json:"more,omitempty"`
	Deleted  int      `json:"deleted,omitempty"`
	Dropped  int      `json:"dropped,omitempty"`
}

func errReply(err error) reply {
	return reply{Code: errCode(err), Error: err.Error()}
}

func (r reply) err() error {
	if r.OK {
		return nil
	}
	if e, ok := codeErr[r.Code]; ok {
		return e
	}
	return fmt.Errorf("%w: %s", ErrInternal, r.Error)
}

// writeFrame writes a 4-byte big-endian length and b.
func writeFrame(w io.Writer, b []byte) error {
	if len(b) > fetchBatchBytes {
		return fmt.Errorf("%w: frame of %d bytes", ErrTooBig, len(b))
	}
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(b))) //nolint:gosec // bounded above
	if _, err := w.Write(n[:]); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}

// readFrame reads a frame of at most limit bytes.
func readFrame(r io.Reader, limit int) ([]byte, error) {
	var n [4]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(n[:])
	if int64(size) > int64(limit) {
		return nil, fmt.Errorf("%w: frame of %d bytes over %d", ErrBadRequest, size, limit)
	}
	b := make([]byte, size)
	_, err := io.ReadFull(r, b)
	return b, err
}

func writeJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeFrame(w, b)
}

func readJSON(r io.Reader, v any) error {
	b, err := readFrame(r, controlMax)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	return nil
}
