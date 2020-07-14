package noise

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/ioutil"
)

// MaxWriteSize is the largest amount for a single write.
const MaxWriteSize = maxPayloadSize

// Frame format: [ len (2 bytes) | auth & nonce (24 bytes) | payload (<= maxPayloadSize bytes) ]
const (
	maxFrameSize   = 4096                                 // maximum frame size (4096)
	maxPayloadSize = maxFrameSize - prefixSize - authSize // maximum payload size
	maxPrefixValue = maxFrameSize - prefixSize            // maximum value contained in the 'len' prefix

	prefixSize = 2  // len prefix size
	authSize   = 24 // noise auth data size
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "deadline exceeded" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

type netError struct {
	err     error
	timeout bool
	temp    bool
}

func (e *netError) Error() string   { return e.err.Error() }
func (e *netError) Timeout() bool   { return e.timeout }
func (e *netError) Temporary() bool { return e.temp }

// ReadWriter implements noise encrypted read writer.
type ReadWriter struct {
	origin io.ReadWriter
	ns     *Noise

	rawInput *bufio.Reader
	input    bytes.Buffer

	rErr error
	rMx  sync.Mutex

	wErr error
	wMx  sync.Mutex
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

	if rw.rErr != nil {
		return 0, rw.rErr
	}

	for {
		ciphertext, err := ReadRawFrame(rw.rawInput)
		if err != nil {
			return 0, rw.processReadError(err)
		}

		plaintext, err := rw.ns.DecryptUnsafe(ciphertext)
		if err != nil {
			return 0, rw.processReadError(err)
		}

		if len(plaintext) == 0 {
			continue
		}

		return ioutil.BufRead(&rw.input, plaintext, p)
	}
}

// processReadError processes error before returning.
// * Ensure error implements net.Error
// * If error is non-temporary, save error in state so further reads will fail.
func (rw *ReadWriter) processReadError(err error) error {
	if nErr, ok := err.(net.Error); ok {
		if !nErr.Temporary() {
			rw.rErr = err
		}
		return err
	}

	err = &netError{
		err:     err,
		timeout: false,
		temp:    false,
	}
	rw.rErr = err
	return err
}

func (rw *ReadWriter) Write(p []byte) (n int, err error) {
	rw.wMx.Lock()
	defer rw.wMx.Unlock()

	if rw.wErr != nil {
		return 0, rw.wErr
	}

	// Check for timeout errors.
	if _, err = rw.origin.Write(nil); err != nil {
		return 0, err
	}

	p = p[:]

	for len(p) > 0 {
		// Enforce max frame size.
		wn := len(p)
		if len(p) > maxPayloadSize {
			wn = maxPayloadSize
		}

		wb, err := WriteRawFrame(rw.origin, rw.ns.EncryptUnsafe(p[:wn]))
		if err != nil {
			// when a short write occurs, it is hard to recover from so we
			// consider it a permanent error
			if len(wb) != 0 {
				err = &netError{
					err:     fmt.Errorf("%v: %w", io.ErrShortWrite, err),
					timeout: false,
					temp:    false,
				}
			}

			// if error is permanent, we record it in the internal state so no
			// further writes occurs
			if !isTemp(err) {
				rw.wErr = err
			}

			return n, err
		}

		n += wn
		p = p[wn:]
	}

	return n, err
}

// Handshake performs a Noise handshake using the provided io.ReadWriter.
func (rw *ReadWriter) Handshake(hsTimeout time.Duration) error {
	errCh := make(chan error, 1)
	go func() {
		if rw.ns.init {
			fmt.Fprintf(os.Stdout, "PERFORMING INITIATOR HANDSHAKE\n")
			err := InitiatorHandshake(rw.ns, rw.rawInput, rw.origin)
			fmt.Fprintf(os.Stdout, "INITIATOR HANDSHAKE ERROR: %v\n", err)
			errCh <- err
		} else {
			fmt.Fprintf(os.Stdout, "PERFORMING RESPONDER HANDSHAKE\n")
			err := ResponderHandshake(rw.ns, rw.rawInput, rw.origin)
			fmt.Fprintf(os.Stdout, "RESPONDER HANDSHAKE ERROR: %v\n", err)
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case err := <-errCh:
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

// InitiatorHandshake performs a noise handshake as an initiator.
func InitiatorHandshake(ns *Noise, r *bufio.Reader, w io.Writer) error {
	for {
		msg, err := ns.MakeHandshakeMessage()
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "TEST: WRITING INITIATOR FRAME")
		if _, err := WriteRawFrame(w, msg); err != nil {
			fmt.Fprintf(os.Stdout, "TEST: ERROR WRITING INITIATOR FRAME: %v\n", err)
			return err
		}
		fmt.Fprintln(os.Stdout, "TEST: WROTE INITIATOR FRAME")
		if ns.HandshakeFinished() {
			fmt.Fprintln(os.Stdout, "TEST: INITIATOR HANDSHAKE FINISHED (1)")
			break
		}
		fmt.Fprintln(os.Stdout, "TEST: READING INITIATOR FRAME")
		res, err := ReadRawFrame(r)
		if err != nil {
			fmt.Fprintf(os.Stdout, "TEST: ERROR READING INITIATOR FRAME: %v\n", err)
			return err
		}
		fmt.Fprintln(os.Stdout, "TEST: READ INITIATOR FRAME")
		if err = ns.ProcessHandshakeMessage(res); err != nil {
			fmt.Fprintf(os.Stdout, "TEST: ERROR PROCESSING INITIATOR HANDSHAKE MESSAGE: %v\n", err)
			return err
		}
		fmt.Fprintln(os.Stdout, "TEST: PROCESSED INITIATOR HANDSHAKE MESSAGE")
		if ns.HandshakeFinished() {
			fmt.Fprintln(os.Stdout, "TEST: INITIATOR HANDSHAKE FINISHED (2)")
			break
		}

		fmt.Fprintln(os.Stdout, "TEST: INITIATOR HANDSHAKE NOT FINISHED, NEW CYCLE...")
	}
	return nil
}

// ResponderHandshake performs a noise handshake as a responder.
func ResponderHandshake(ns *Noise, r *bufio.Reader, w io.Writer) error {
	for {
		fmt.Fprintln(os.Stdout, "TEST: READING RESPONDER FRAME")
		msg, err := ReadRawFrame(r)
		if err != nil {
			fmt.Fprintf(os.Stdout, "TEST: ERROR READING RESPONDER FRAME: %v\n", err)
			return err
		}
		fmt.Fprintln(os.Stdout, "TEST: READ RESPONDER FRAME")
		if err := ns.ProcessHandshakeMessage(msg); err != nil {
			fmt.Fprintf(os.Stdout, "TEST: ERROR PROCESSING RESPONDER HANDSHAKE MESSAGE: %v\n", err)
			return err
		}
		fmt.Fprintln(os.Stdout, "TEST: PROCESSED RESPONDER HANDSHAKE MESSAGE")
		if ns.HandshakeFinished() {
			fmt.Fprintln(os.Stdout, "TEST: RESPONDER HANDSHAKE FINISHED (1)")
			break
		}
		res, err := ns.MakeHandshakeMessage()
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "TEST: WRITING RESPONDER HANDSHAKE MESSAGE")
		if _, err := WriteRawFrame(w, res); err != nil {
			fmt.Fprintf(os.Stdout, "TEST: ERROR WRITING RESPONDER HANDSHAKE MESSAGE: %v\n", err)
			return err
		}
		fmt.Fprintln(os.Stdout, "TEST: WROTE RESPONDER HANDSHAKE MESSAGE")
		if ns.HandshakeFinished() {
			fmt.Fprintln(os.Stdout, "TEST: RESPONDER HANDSHAKE FINISHED (2)")
			break
		}
		fmt.Fprintln(os.Stdout, "TEST: RESPONDER HANDSHAKE NOT FINISHED, NEW CYCLE...")
	}
	return nil
}

// WriteRawFrame writes a raw frame (data prefixed with a uint16 len).
// It returns the bytes written.
func WriteRawFrame(w io.Writer, p []byte) ([]byte, error) {
	buf := make([]byte, prefixSize+len(p))
	binary.BigEndian.PutUint16(buf, uint16(len(p)))
	copy(buf[prefixSize:], p)

	n, err := w.Write(buf)
	return buf[:n], err
}

// ReadRawFrame attempts to read a raw frame from a buffered reader.
func ReadRawFrame(r *bufio.Reader) (p []byte, err error) {
	fmt.Fprintln(os.Stdout, "TEST: PEEKING PREFIXB")
	prefixB, err := r.Peek(prefixSize)
	if err != nil {
		fmt.Fprintf(os.Stdout, "TEST: ERROR PEEKING PREFIXB: %v\n", err)
		return nil, err
	}
	fmt.Fprintln(os.Stdout, "TEST: PEEKED PREFIXB")

	// obtain payload size
	prefix := int(binary.BigEndian.Uint16(prefixB))
	if prefix > maxPrefixValue {
		fmt.Fprintln(os.Stdout, "TEST: PREFIX > MAX_PREFIX_VALUE")
		return nil, &netError{
			err:     fmt.Errorf("noise prefix value %dB exceeds maximum %dB", prefix, maxPrefixValue),
			timeout: false,
			temp:    false,
		}
	}

	// obtain payload
	fmt.Fprintln(os.Stdout, "PEEKING B")
	b, err := r.Peek(prefixSize + prefix)
	if err != nil {
		fmt.Fprintf(os.Stdout, "TEST: ERROR PEEKING B: %v\n", err)
		return nil, err
	}
	fmt.Fprintln(os.Stdout, "TEST: DISCARDING PREFIXSIZE + PREFIX")
	if _, err := r.Discard(prefixSize + prefix); err != nil {
		fmt.Fprintf(os.Stdout, "TEST: ERROR DISCARDING: %v\n", err)
		panic(fmt.Errorf("unexpected error when discarding %d bytes: %v", prefixSize+prefix, err))
	}
	fmt.Fprintln(os.Stdout, "TEST: DISCARDED PREFIXSIZE + PREFIX")

	return b[prefixSize:], nil
}

func isTemp(err error) bool {
	if netErr, ok := err.(net.Error); ok && netErr.Temporary() {
		return true
	}
	return false
}
