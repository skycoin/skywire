// Package handshake pkg/transport/network/handshake/handshake_test.go
package handshake

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

const (
	port1 = 10
	port2 = 11
)

func TestHandshake(t *testing.T) {
	type hsResult struct {
		lAddr dmsg.Addr
		rAddr dmsg.Addr
		err   error
	}

	for i := byte(0); i < 64; i++ {
		initPK, initSK, err := cipher.GenerateDeterministicKeyPair(append([]byte("init"), i))
		require.NoError(t, err)

		respPK, _, err := cipher.GenerateDeterministicKeyPair(append([]byte("resp"), i))
		require.NoError(t, err)

		iAddr := dmsg.Addr{PK: initPK, Port: port1}
		rAddr := dmsg.Addr{PK: respPK, Port: port2}

		initC, respC := net.Pipe()

		deadline := time.Now().Add(Timeout)

		respCh := make(chan hsResult, 1)

		go func() {
			defer close(respCh)

			respHS := ResponderHandshake(func(f2 Frame2) error {
				if f2.SrcAddr.PK != initPK {
					return errors.New("unexpected src addr pk")
				}
				if f2.DstAddr.PK != respPK {
					return errors.New("unexpected dst addr pk")
				}
				return nil
			})

			lAddr, rAddr, err := respHS(respC, deadline)
			respCh <- hsResult{lAddr: lAddr, rAddr: rAddr, err: err}
		}()

		initHS := InitiatorHandshake(initSK, iAddr, rAddr)

		var initR hsResult
		initR.lAddr, initR.rAddr, initR.err = initHS(initC, deadline)

		assert.NoError(t, err)
		assert.Equal(t, initR.lAddr, iAddr)
		assert.Equal(t, initR.rAddr, rAddr)

		rr := <-respCh

		assert.NoError(t, rr.err)
		assert.Equal(t, rr.lAddr, rAddr)
		assert.Equal(t, rr.rAddr, iAddr)

		assert.NoError(t, initC.Close())
		assert.NoError(t, respC.Close())
	}
}

// chunkedReader returns its payload in fixed-size chunks, one per Read, to
// emulate cmux's sniff-replay: the demux peeks the first bytes to classify the
// protocol, then replays them as a separate Read from the live remainder. This
// is also just legal io.Reader behavior — a Read may return fewer bytes than
// asked even when more are available.
type chunkedReader struct {
	data  []byte
	chunk int
}

func (c *chunkedReader) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := c.chunk
	if n > len(c.data) {
		n = len(c.data)
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, c.data[:n])
	c.data = c.data[n:]
	return n, nil
}

// TestReadFrame0Chunked guards the stcpr+WS cmux regression: frame0 (Message)
// arriving split across reads — e.g. cmux replays the first 8 sniffed bytes,
// then the 9th comes live — must still parse. The old single-Read check failed
// with "not enough bytes read", breaking every inbound stcpr handshake on a
// cmux-equipped (transport-port-unified) visor.
func TestReadFrame0Chunked(t *testing.T) {
	for _, chunk := range []int{1, 8, len(Message), len(Message) + 5} {
		r := &chunkedReader{data: []byte(Message), chunk: chunk}
		require.NoError(t, readFrame0(r), "chunk size %d", chunk)
	}
}
