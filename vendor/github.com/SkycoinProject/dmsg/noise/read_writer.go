package noise

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/ioutil"
)

// Frame format: [ len (2 bytes) | auth (16 bytes) | payload (<= maxPayloadSize bytes) ]
const (
	maxFrameSize   = 4096                                 // maximum frame size (4096)
	maxPayloadSize = maxFrameSize - prefixSize - authSize // maximum payload size
	maxPrefixValue = maxFrameSize - prefixSize            // maximum value contained in the 'len' prefix

	prefixSize = 2  // len prefix size
	authSize   = 16 // noise auth data size
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "deadline exceeded" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

type netError struct{ Err error }

func (e *netError) Error() string { return e.Err.Error() }
func (netError) Timeout() bool    { return false }
func (netError) Temporary() bool  { return true }

// ReadWriter implements noise encrypted read writer.
type ReadWriter struct {
	origin io.ReadWriter
	ns     *Noise

	rawInput *bufio.Reader
	input    bytes.Buffer

	rMx sync.Mutex
	wMx sync.Mutex
}

// NewReadWriter constructs a new ReadWriter.
func NewReadWriter(rw io.ReadWriter, ns *Noise) *ReadWriter {
	return &ReadWriter{
		origin:   rw,
		ns:       ns,
		rawInput: bufio.NewReaderSize(rw, maxFrameSize*2), // can fit 2 frames.
	}
}

func (rw *ReadWriter) Read(p []byte) (int, error) {
	rw.rMx.Lock()
	defer rw.rMx.Unlock()

	if rw.input.Len() > 0 {
		return rw.input.Read(p)
	}
	ciphertext, err := rw.readPacket()
	if err != nil {
		return 0, err
	}
	plaintext, err := rw.ns.DecryptUnsafe(ciphertext)
	if err != nil {
		return 0, &netError{Err: err}
	}
	if len(plaintext) == 0 {
		return 0, nil
	}
	return ioutil.BufRead(&rw.input, plaintext, p)
}

func (rw *ReadWriter) readPacket() ([]byte, error) {
	return readWithBuf(rw.rawInput)
}

func readWithBuf(in *bufio.Reader) (out []byte, err error) {
	prefixB, err := in.Peek(prefixSize)
	if err != nil {
		return nil, err
	}

	// obtain payload size
	prefix := int(binary.BigEndian.Uint16(prefixB))
	if prefix > maxPrefixValue {
		return nil, &netError{
			Err: fmt.Errorf("noise prefix value %dB exceeds maximum %dB", prefix, maxPrefixValue),
		}
	}

	// obtain payload
	b, err := in.Peek(prefixSize + prefix)
	if err != nil {
		return nil, err
	}
	if _, err := in.Discard(prefixSize + prefix); err != nil {
		panic(fmt.Errorf("unexpected error when discarding %d bytes: %v", prefixSize+prefix, err))
	}
	return b[prefixSize:], nil
}

func (rw *ReadWriter) Write(p []byte) (n int, err error) {
	rw.wMx.Lock()
	defer rw.wMx.Unlock()

	// Enforce max write size.
	if len(p) > maxPayloadSize {
		p, err = p[:maxPayloadSize], io.ErrShortWrite
	}
	if err := rw.writeFrame(rw.ns.EncryptUnsafe(p)); err != nil {
		return 0, err
	}
	return len(p), err
}

func (rw *ReadWriter) writeFrame(p []byte) error {
	buf := make([]byte, prefixSize+len(p))
	binary.BigEndian.PutUint16(buf, uint16(len(p)))
	copy(buf[prefixSize:], p)
	_, err := rw.origin.Write(buf)
	return err
}

// Handshake performs a Noise handshake using the provided io.ReadWriter.
func (rw *ReadWriter) Handshake(hsTimeout time.Duration) error {
	doneChan := make(chan error)
	go func() {
		if rw.ns.init {
			doneChan <- rw.initiatorHandshake()
		} else {
			doneChan <- rw.responderHandshake()
		}
	}()
	select {
	case err := <-doneChan:
		return err
	case <-time.After(hsTimeout):
		return timeoutError{}
	}
}

// LocalStatic returns the local static public key.
func (rw *ReadWriter) LocalStatic() cipher.PubKey {
	return rw.ns.LocalStatic()
}

// RemoteStatic returns the remote static public key.
func (rw *ReadWriter) RemoteStatic() cipher.PubKey {
	return rw.ns.RemoteStatic()
}

func (rw *ReadWriter) initiatorHandshake() error {
	for {
		msg, err := rw.ns.HandshakeMessage()
		if err != nil {
			return err
		}
		if err := rw.writeFrame(msg); err != nil {
			return err
		}
		if rw.ns.HandshakeFinished() {
			break
		}
		res, err := rw.readPacket()
		if err != nil {
			return err
		}
		if err = rw.ns.ProcessMessage(res); err != nil {
			return err
		}
		if rw.ns.HandshakeFinished() {
			break
		}
	}
	return nil
}

func (rw *ReadWriter) responderHandshake() error {
	for {
		msg, err := rw.readPacket()
		if err != nil {
			return err
		}
		if err := rw.ns.ProcessMessage(msg); err != nil {
			return err
		}
		if rw.ns.HandshakeFinished() {
			break
		}
		res, err := rw.ns.HandshakeMessage()
		if err != nil {
			return err
		}
		if err := rw.writeFrame(res); err != nil {
			return err
		}
		if rw.ns.HandshakeFinished() {
			break
		}
	}
	return nil
}
