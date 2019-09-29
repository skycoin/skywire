package dmsg

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/SkycoinProject/dmsg/cipher"
	"io"
	"math"
	"sync/atomic"
	"time"
)

const (
	// Type returns the transport type string.
	Type = "dmsg"

	// HandshakePayloadVersion contains payload version to maintain compatibility with future versions
	// of HandshakeData format.
	HandshakePayloadVersion = "2.0"

	maxFwdPayLen = math.MaxUint16 // maximum len of FWD payload
	headerLen    = 5              // fType(1 byte), chID(2 byte), payLen(2 byte)
)

var (
	// TransportHandshakeTimeout defines the duration a transport handshake should take.
	TransportHandshakeTimeout = time.Second * 10

	// AcceptBufferSize defines the size of the accepts buffer.
	AcceptBufferSize = 20
)

// Addr implements net.Addr for dmsg addresses.
type Addr struct {
	PK   cipher.PubKey `json:"public_key"`
	Port uint16        `json:"port"`
}

// Network returns "dmsg"
func (Addr) Network() string {
	return Type
}

// String returns public key and port of node split by colon.
func (a Addr) String() string {
	if a.Port == 0 {
		return fmt.Sprintf("%s:~", a.PK)
	}
	return fmt.Sprintf("%s:%d", a.PK, a.Port)
}

// HandshakeData represents format of payload sent with REQUEST frames.
type HandshakeData struct {
	Version  string `json:"version"` // just in case the struct changes.
	InitAddr Addr   `json:"init_address"`
	RespAddr Addr   `json:"resp_address"`

	// Window is the advertised read window size.
	Window int32 `json:"window"`
}

func marshalHandshakeData(p HandshakeData) []byte {
	b, err := json.Marshal(p)
	if err != nil {
		panic(fmt.Errorf("marshalHandshakeData: %v", err))
	}
	return b
}

func unmarshalHandshakeData(b []byte) (HandshakeData, error) {
	var p HandshakeData
	err := json.Unmarshal(b, &p)
	return p, err
}

// determines whether the transport ID is of an initiator or responder.
func isInitiatorID(tpID uint16) bool { return tpID%2 == 0 }

func randID(initiator bool) uint16 {
	var id uint16
	for {
		id = binary.BigEndian.Uint16(cipher.RandByte(2))
		if initiator && id%2 == 0 || !initiator && id%2 != 0 {
			return id
		}
	}
}

// serveCount records the number of dmsg.Servers connected
var serveCount int64

func incrementServeCount() int64 { return atomic.AddInt64(&serveCount, 1) }
func decrementServeCount() int64 { return atomic.AddInt64(&serveCount, -1) }

// FrameType represents the frame type.
type FrameType byte

func (ft FrameType) String() string {
	var names = []string{
		RequestType: "REQUEST",
		AcceptType:  "ACCEPT",
		CloseType:   "CLOSE",
		FwdType:     "FWD",
		AckType:     "ACK",
		OkType:      "OK",
	}
	if int(ft) >= len(names) {
		return fmt.Sprintf("UNKNOWN:%d", ft)
	}
	return names[ft]
}

// Frame types.
const (
	OkType      = FrameType(0x0)
	RequestType = FrameType(0x1)
	AcceptType  = FrameType(0x2)
	CloseType   = FrameType(0x3)
	FwdType     = FrameType(0xa)
	AckType     = FrameType(0xb)
)

// Reasons for closing frames
const (
	PlaceholderReason = iota
)

// Frame is the dmsg data unit.
type Frame []byte

// MakeFrame creates a new Frame.
func MakeFrame(ft FrameType, chID uint16, pay []byte) Frame {
	f := make(Frame, headerLen+len(pay))
	f[0] = byte(ft)
	binary.BigEndian.PutUint16(f[1:3], chID)
	binary.BigEndian.PutUint16(f[3:5], uint16(len(pay)))
	copy(f[5:], pay)
	return f
}

// Type returns the frame's type.
func (f Frame) Type() FrameType { return FrameType(f[0]) }

// TpID returns the frame's tp_id.
func (f Frame) TpID() uint16 { return binary.BigEndian.Uint16(f[1:3]) }

// PayLen returns the expected payload len.
func (f Frame) PayLen() int { return int(binary.BigEndian.Uint16(f[3:5])) }

// Pay returns the payload.
func (f Frame) Pay() []byte { return f[headerLen:] }

// Disassemble splits the frame into fields.
func (f Frame) Disassemble() (ft FrameType, id uint16, p []byte) {
	return f.Type(), f.TpID(), f.Pay()
}

// String implements io.Stringer
func (f Frame) String() string {
	var p string
	switch f.Type() {
	case AckType:
		offset, err := disassembleAckPayload(f.Pay())
		if err != nil {
			p = fmt.Sprintf("<offset:%v>", err)
		} else {
			p = fmt.Sprintf("<offset:%d>", offset)
		}
	}
	return fmt.Sprintf("<type:%s><id:%d><size:%d>%s", f.Type(), f.TpID(), f.PayLen(), p)
}

type disassembledFrame struct {
	Type FrameType
	TpID uint16
	Pay  []byte
}

// read and disassembles frame from reader
func readFrame(r io.Reader) (f Frame, df disassembledFrame, err error) {
	f = make(Frame, headerLen)
	if _, err = io.ReadFull(r, f); err != nil {
		return
	}
	f = append(f, make([]byte, f.PayLen())...)
	if _, err = io.ReadFull(r, f[headerLen:]); err != nil {
		return
	}
	t, id, p := f.Disassemble()
	df = disassembledFrame{Type: t, TpID: id, Pay: p}
	return
}

type writeError struct{ error }

func (e *writeError) Error() string { return "write error: " + e.error.Error() }

// TODO(evanlinjin): determine if this is still needed.
//func isWriteError(err error) bool {
//	_, ok := err.(*writeError)
//	return ok
//}

func writeFrame(w io.Writer, f Frame) error {
	_, err := w.Write(f)
	if err != nil {
		return &writeError{err}
	}
	return nil
}

func writeRequestFrame(w io.Writer, id uint16, lAddr, rAddr Addr, lWindow int32) error {
	return writeFrame(w, MakeFrame(RequestType, id, marshalHandshakeData(
		HandshakeData{
			Version:  HandshakePayloadVersion,
			InitAddr: lAddr,
			RespAddr: rAddr,
			Window:   lWindow,
		})))
}

func writeAcceptFrame(w io.Writer, id uint16, lAddr, rAddr Addr, lWindow int32) error {
	return writeFrame(w, MakeFrame(AcceptType, id, marshalHandshakeData(
		HandshakeData{
			Version:  HandshakePayloadVersion,
			InitAddr: rAddr,
			RespAddr: lAddr,
			Window:   lWindow,
		})))
}

func writeFwdFrame(w io.Writer, id uint16, p []byte) error {
	return writeFrame(w, MakeFrame(FwdType, id, p))
}

func writeAckFrame(w io.Writer, id uint16, offset uint16) error {
	p := make([]byte, 2)
	binary.BigEndian.PutUint16(p, offset)
	return writeFrame(w, MakeFrame(AckType, id, p))
}

func disassembleAckPayload(p []byte) (offset uint16, err error) {
	if len(p) != 2 {
		return 0, errors.New("invalid ACK payload size")
	}
	return binary.BigEndian.Uint16(p), nil
}

func writeCloseFrame(w io.Writer, id uint16, reason byte) error {
	return writeFrame(w, MakeFrame(CloseType, id, []byte{reason}))
}
