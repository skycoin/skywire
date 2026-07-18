// Package wasmhv pkg/wasmhv/gobrpc.go c3-vis-wasm
package wasmhv

import (
	"encoding/gob"
	"errors"
	"io"
	"sync"
)

// gobRPCClient is a minimal client that speaks the EXACT net/rpc gob wire
// protocol (Request header + args, then Response header + reply) — so it talks
// to the visors' net/rpc server unchanged — but WITHOUT importing net/rpc, which
// transitively pulls net/http (broken on the TinyGo js target). This is what
// lets the wasm hypervisor core compile under TinyGo.
//
// One call at a time, serialized by mu — which is exactly how the hypervisor
// core uses it (one outstanding RPC per visor at a time).
type gobRPCClient struct {
	conn io.ReadWriteCloser
	enc  *gob.Encoder
	dec  *gob.Decoder
	mu   sync.Mutex
	seq  uint64
}

// rpcRequest / rpcResponse mirror net/rpc.Request / net/rpc.Response by field
// name + type, so gob encodes/decodes them wire-identically (gob matches struct
// fields by name and ignores the Go type name).
type rpcRequest struct {
	ServiceMethod string
	Seq           uint64
}

type rpcResponse struct {
	ServiceMethod string
	Seq           uint64
	Error         string
}

func newGobRPCClient(conn io.ReadWriteCloser) *gobRPCClient {
	return &gobRPCClient{conn: conn, enc: gob.NewEncoder(conn), dec: gob.NewDecoder(conn)}
}

// Call performs a synchronous net/rpc-compatible call: gob-encode the request
// header + args, then gob-decode the response header + reply. On a server-side
// error the server still writes an (empty) reply body, which is read and
// discarded so the gob stream stays aligned for the next call.
func (c *gobRPCClient) Call(serviceMethod string, args, reply interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.seq++
	if err := c.enc.Encode(&rpcRequest{ServiceMethod: serviceMethod, Seq: c.seq}); err != nil {
		return err
	}
	if err := c.enc.Encode(args); err != nil {
		return err
	}

	var resp rpcResponse
	if err := c.dec.Decode(&resp); err != nil {
		return err
	}
	if resp.Error != "" {
		// The server still writes an (empty) reply body after an error response;
		// read + discard it so the gob stream stays aligned for the next call.
		// net/rpc sends struct{}{} (invalidRequest) as that body — decode into a
		// matching throwaway pointer. (gob's Decode(nil) would also discard, but
		// `go vet` flags the literal nil as "call of Decode passes non-pointer".)
		var discard struct{}
		_ = c.dec.Decode(&discard) //nolint:errcheck
		return errors.New(resp.Error)
	}
	return c.dec.Decode(reply)
}

// Close closes the underlying connection.
func (c *gobRPCClient) Close() error { return c.conn.Close() }
