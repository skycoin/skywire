// Package handshake handles handhakes
package handshake

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

const (
	// Timeout is the default timeout for a handshake.
	Timeout = time.Second * 10

	// NonceSize is the size of the nonce for the handshake.
	NonceSize = 16

	// Message is sent by initiator to start a handshake.
	Message = "get_nonce"
)

// Error occurs when the handshake fails.
type Error string

// Error implements error.
func (err Error) Error() string {
	return fmt.Sprintln("handshake failed:", string(err))
}

// IsHandshakeError determines whether the error occurred during the handshake.
func IsHandshakeError(err error) bool {
	_, ok := err.(Error)
	return ok
}

// middleware to add deadline and Error to handshakes.
func handshakeMiddleware(origin Handshake) Handshake {
	return func(conn net.Conn, deadline time.Time) (lAddr, rAddr dmsg.Addr, err error) {
		if err = conn.SetDeadline(deadline); err != nil {
			return
		}

		if lAddr, rAddr, err = origin(conn, deadline); err != nil {
			err = Error(err.Error())
			return
		}

		// reset deadline
		err = conn.SetDeadline(time.Time{})

		return
	}
}

// Handshake represents a handshake.
type Handshake func(conn net.Conn, deadline time.Time) (lAddr, rAddr dmsg.Addr, err error)

// InitiatorHandshake creates the handshake logic on the initiator's side.
func InitiatorHandshake(lSK cipher.SecKey, localAddr, remoteAddr dmsg.Addr) Handshake {
	return handshakeMiddleware(func(conn net.Conn, deadline time.Time) (lAddr, rAddr dmsg.Addr, err error) {
		if err = writeFrame0(conn); err != nil {
			return dmsg.Addr{}, dmsg.Addr{}, err
		}

		var f1 Frame1
		if f1, err = readFrame1(conn); err != nil {
			return dmsg.Addr{}, dmsg.Addr{}, err
		}

		f2 := Frame2{SrcAddr: localAddr, DstAddr: remoteAddr, Nonce: f1.Nonce}
		if err = f2.Sign(lSK); err != nil {
			return dmsg.Addr{}, dmsg.Addr{}, err
		}

		if err = writeFrame2(conn, f2); err != nil {
			return dmsg.Addr{}, dmsg.Addr{}, err
		}

		var f3 Frame3
		if f3, err = readFrame3(conn); err != nil {
			return dmsg.Addr{}, dmsg.Addr{}, err
		}

		if !f3.OK {
			err = fmt.Errorf("handshake rejected: %s", f3.ErrMsg)
			return dmsg.Addr{}, dmsg.Addr{}, err
		}

		lAddr = localAddr
		rAddr = remoteAddr

		return lAddr, rAddr, nil
	})
}

// CheckF2 checks second frame of handshake
type CheckF2 = func(f2 Frame2) error

// MakeF2PortChecker returns new CheckF2 function that will use
// port checker to check port in Frame2
func MakeF2PortChecker(portChecker func(port uint16) error) CheckF2 {
	return func(f2 Frame2) error {
		return portChecker(f2.DstAddr.Port)
	}
}

// ResponderHandshake creates the handshake logic on the responder's side.
func ResponderHandshake(checkF2 CheckF2) Handshake {
	return handshakeMiddleware(func(conn net.Conn, deadline time.Time) (lAddr, rAddr dmsg.Addr, err error) {
		if err = readFrame0(conn); err != nil {
			return dmsg.Addr{}, dmsg.Addr{}, err
		}

		var nonce [NonceSize]byte
		copy(nonce[:], cipher.RandByte(NonceSize))

		if err = writeFrame1(conn, nonce); err != nil {
			return dmsg.Addr{}, dmsg.Addr{}, err
		}

		var f2 Frame2
		if f2, err = readFrame2(conn); err != nil {
			return dmsg.Addr{}, dmsg.Addr{}, err
		}

		if err = f2.Verify(nonce); err != nil {
			return dmsg.Addr{}, dmsg.Addr{}, err
		}

		if err = checkF2(f2); err != nil {
			_ = writeFrame3(conn, err) // nolint:errcheck
			return dmsg.Addr{}, dmsg.Addr{}, err
		}

		lAddr = f2.DstAddr
		rAddr = f2.SrcAddr
		if err = writeFrame3(conn, nil); err != nil {
			return dmsg.Addr{}, dmsg.Addr{}, err
		}

		return lAddr, rAddr, nil
	})
}

// Frame1 is the first frame of the handshake (Resp -> Init).
type Frame1 struct {
	Nonce [NonceSize]byte
}

// Frame2 is the second frame of the handshake (Init -> Resp).
type Frame2 struct {
	SrcAddr dmsg.Addr
	DstAddr dmsg.Addr
	Nonce   [NonceSize]byte
	Sig     cipher.Sig
}

// Sign signs Frame2.
func (f2 *Frame2) Sign(srcSK cipher.SecKey) error {
	pk, err := srcSK.PubKey()
	if err != nil {
		return err
	}

	f2.SrcAddr.PK = pk
	f2.Sig = cipher.Sig{}

	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(f2); err != nil {
		return err
	}

	sig, err := cipher.SignPayload(b.Bytes(), srcSK)
	if err != nil {
		return err
	}

	f2.Sig = sig

	return nil
}

// Verify verifies the signature field within Frame2.
func (f2 Frame2) Verify(nonce [NonceSize]byte) error {
	if f2.Nonce != nonce {
		return errors.New("unexpected nonce")
	}

	sig := f2.Sig
	f2.Sig = cipher.Sig{}

	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(f2); err != nil {
		return err
	}

	return cipher.VerifyPubKeySignedPayload(f2.SrcAddr.PK, sig, b.Bytes())
}

// Frame3 is the third frame of the handshake. (Resp -> Init)
type Frame3 struct {
	OK     bool
	ErrMsg string
}

func writeFrame0(w io.Writer) error {
	n, err := w.Write([]byte(Message))
	if err != nil {
		return err
	}

	if n != len(Message) {
		return fmt.Errorf("not enough bytes written")
	}

	return nil
}

func readFrame0(r io.Reader) error {
	buf := make([]byte, len(Message))

	n, err := r.Read(buf)
	if err != nil {
		return err
	}

	if n != len(Message) {
		return fmt.Errorf("not enough bytes read")
	}

	if string(buf[:n]) != Message {
		return fmt.Errorf("bad handshake message: %v", string(buf[:n]))
	}

	return nil
}

func writeFrame1(w io.Writer, nonce [NonceSize]byte) error {
	return json.NewEncoder(w).Encode(Frame1{Nonce: nonce})
}

func readFrame1(r io.Reader) (Frame1, error) {
	var f1 Frame1
	err := json.NewDecoder(r).Decode(&f1)

	return f1, err
}

func writeFrame2(w io.Writer, f2 Frame2) error {
	return json.NewEncoder(w).Encode(f2)
}

func readFrame2(r io.Reader) (Frame2, error) {
	var f2 Frame2
	err := json.NewDecoder(r).Decode(&f2)

	return f2, err
}

func writeFrame3(w io.Writer, err error) error {
	f3 := Frame3{OK: true}
	if err != nil {
		f3.OK = false
		f3.ErrMsg = err.Error()
	}

	return json.NewEncoder(w).Encode(f3)
}

func readFrame3(r io.Reader) (Frame3, error) {
	var f3 Frame3
	err := json.NewDecoder(r).Decode(&f3)

	return f3, err
}
