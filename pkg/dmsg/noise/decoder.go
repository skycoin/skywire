package noise

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrReadDetached is returned by Read once the stream has moved to a Decoder.
var ErrReadDetached = errors.New("noise: reads moved to a decoder")

// Decoder decrypts noise frames from a stream pushed to it in pieces of any
// size, for a reader driven by arriving data instead of a goroutine in Read.
type Decoder struct {
	ns    *Noise
	buf   []byte // the start of a frame that is not yet complete
	plain []byte // reused plaintext of the last frame
	err   error
}

// Feed decrypts every frame that p completes and passes the plaintext of each
// to emit, which must not keep it. An error is final.
func (d *Decoder) Feed(p []byte, emit func(plaintext []byte)) error {
	if d.err != nil {
		return d.err
	}
	in := p
	if len(d.buf) > 0 {
		d.buf = append(d.buf, p...)
		in = d.buf
	}
	for len(in) >= prefixSize {
		size := int(binary.BigEndian.Uint16(in))
		if size > maxPrefixValue {
			d.err = fmt.Errorf("noise prefix value %dB exceeds maximum %dB", size, maxPrefixValue)
			return d.err
		}
		if len(in) < prefixSize+size {
			break
		}
		plain, err := d.ns.decryptAppend(d.plain[:0], in[prefixSize:prefixSize+size])
		if err != nil {
			d.err = err
			return err
		}
		d.plain = plain
		in = in[prefixSize+size:]
		if len(plain) > 0 {
			emit(plain)
		}
	}
	// Keep the partial frame. copy handles in overlapping d.buf.
	if len(in) > cap(d.buf) {
		d.buf = make([]byte, 0, maxFrameSize)
	}
	d.buf = d.buf[:copy(d.buf[:len(in)], in)]
	return nil
}

// Decoder moves the rest of the stream to a Decoder and returns it with the
// plaintext rw had decrypted but not yet returned. rw must not be read after.
func (rw *ReadWriter) Decoder() (*Decoder, []byte) {
	rw.rMx.Lock()
	defer rw.rMx.Unlock()

	d := &Decoder{ns: rw.ns, err: rw.rErr}
	var pending []byte
	if rw.input.Len() > 0 {
		pending = append(pending, rw.input.Bytes()...)
		rw.input.Reset()
	}
	if n := rw.rawInput.Buffered(); n > 0 {
		b, err := rw.rawInput.Peek(n)
		if err == nil {
			d.buf = append(make([]byte, 0, max(n, maxFrameSize)), b...)
			_, _ = rw.rawInput.Discard(n) //nolint:errcheck
		}
	}
	if rw.rErr == nil {
		rw.rErr = ErrReadDetached
	}
	return d, pending
}

// Decoder moves the rest of the stream to a Decoder, as ReadWriter.Decoder.
func (c *Conn) Decoder() (*Decoder, []byte) {
	return c.ns.Decoder()
}
