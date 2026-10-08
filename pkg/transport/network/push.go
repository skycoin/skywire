package network

import "github.com/skycoin/skywire/pkg/dmsg/noise"

// PushSource is a conn whose data can be taken as it arrives, without a
// goroutine blocked in Read. A KCP session is one.
type PushSource interface {
	// SetReadNotify sets fn to run whenever data may be ready or the conn
	// ended. fn must not block.
	SetReadNotify(fn func())
	// TryRead returns ready data, 0 with no error when there is none, and the
	// conn's error once it has ended.
	TryRead(b []byte) (int, error)
}

// PushReader takes a transport's decrypted stream as it arrives.
type PushReader interface {
	// SetNotify sets fn to run whenever data may be ready or the conn ended.
	SetNotify(fn func())
	// ReadPlain reads what is ready into buf, decrypts it and passes the
	// plaintext to emit. It returns 0 with no error when nothing was ready.
	ReadPlain(buf []byte, emit func([]byte)) (int, error)
}

type pushReader struct {
	src     PushSource
	dec     *noise.Decoder
	pending []byte // plaintext the noise reader held when reads moved here
}

// PushReaderOf moves tp's reads to a PushReader if its conn supports one.
// tp must not be read once it returns true.
func PushReaderOf(tp Transport) (PushReader, bool) {
	if p, ok := tp.(interface{ PushReader() (PushReader, bool) }); ok {
		return p.PushReader()
	}
	return nil, false
}

// PushReader moves the transport's reads to a PushReader if its conn supports
// one, as PushReaderOf.
func (t *transport) PushReader() (PushReader, bool) {
	src, ok := t.rawConn.(PushSource)
	if !ok {
		return nil, false
	}
	nc, ok := t.Conn.(*noise.Conn)
	if !ok {
		return nil, false
	}
	dec, pending := nc.Decoder()
	return &pushReader{src: src, dec: dec, pending: pending}, true
}

func (r *pushReader) SetNotify(fn func()) { r.src.SetReadNotify(fn) }

func (r *pushReader) ReadPlain(buf []byte, emit func([]byte)) (int, error) {
	if p := r.pending; p != nil {
		r.pending = nil
		emit(p)
		return len(p), nil
	}
	n, err := r.src.TryRead(buf)
	if n > 0 {
		if ferr := r.dec.Feed(buf[:n], emit); ferr != nil {
			return n, ferr
		}
	}
	return n, err
}
