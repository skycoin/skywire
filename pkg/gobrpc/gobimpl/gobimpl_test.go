package gobimpl_test

import (
	"errors"
	"io"
	"net"
	"net/rpc"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/gobrpc/gobimpl"
)

// Arith is a tiny RPC service used by both the stdlib net/rpc server and the
// gobimpl server, so the same client code can drive either.
type Arith struct{}

// Args / Reply are the RPC request/reply bodies.
type Args struct{ A, B int }

// Reply carries the computed result.
type Reply struct{ C int }

// Multiply is a normal (value-arg) method.
func (Arith) Multiply(args Args, reply *Reply) error {
	reply.C = args.A * args.B
	return nil
}

// Boom always errors, to exercise the error-response path (which still writes
// a body that the client must discard to stay aligned).
func (Arith) Boom(_ Args, _ *Reply) error { return errors.New("boom") }

// PtrArg exercises a pointer-typed argument.
func (Arith) PtrArg(args *Args, reply *Reply) error {
	reply.C = args.A + args.B
	return nil
}

// pipePair returns two ends of an in-memory duplex connection.
func pipePair() (io.ReadWriteCloser, io.ReadWriteCloser) {
	c1, c2 := net.Pipe()
	return c1, c2
}

// TestGobimplClient_ToStdlibServer drives a gobimpl client against a real
// net/rpc server — proving the client's request wire format is correct.
func TestGobimplClient_ToStdlibServer(t *testing.T) {
	srvConn, cliConn := pipePair()

	srv := rpc.NewServer()
	if err := srv.Register(Arith{}); err != nil {
		t.Fatalf("stdlib register: %v", err)
	}
	go srv.ServeConn(srvConn)

	c := gobimpl.NewClient(cliConn)
	defer c.Close() //nolint:errcheck

	var reply Reply
	if err := c.Call("Arith.Multiply", Args{A: 6, B: 7}, &reply); err != nil {
		t.Fatalf("Multiply: %v", err)
	}
	if reply.C != 42 {
		t.Fatalf("Multiply = %d, want 42", reply.C)
	}

	// Error path: the server returns an error; client must surface it AND
	// keep the stream aligned for the subsequent call.
	err := c.Call("Arith.Boom", Args{}, &Reply{})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("Boom err = %v, want \"boom\"", err)
	}

	// Stream still aligned after the error?
	reply = Reply{}
	if err := c.Call("Arith.Multiply", Args{A: 3, B: 4}, &reply); err != nil {
		t.Fatalf("post-error Multiply: %v", err)
	}
	if reply.C != 12 {
		t.Fatalf("post-error Multiply = %d, want 12", reply.C)
	}
}

// TestStdlibClient_ToGobimplServer drives a real net/rpc client against a
// gobimpl server — proving the server's response wire format AND reflection
// dispatch are correct (value args, pointer args, and the error path).
func TestStdlibClient_ToGobimplServer(t *testing.T) {
	srvConn, cliConn := pipePair()

	srv := gobimpl.NewServer()
	if err := srv.Register(Arith{}); err != nil {
		t.Fatalf("gobimpl register: %v", err)
	}
	go srv.ServeConn(srvConn)

	c := rpc.NewClient(cliConn)
	defer c.Close() //nolint:errcheck

	var reply Reply
	if err := c.Call("Arith.Multiply", Args{A: 6, B: 7}, &reply); err != nil {
		t.Fatalf("Multiply: %v", err)
	}
	if reply.C != 42 {
		t.Fatalf("Multiply = %d, want 42", reply.C)
	}

	reply = Reply{}
	if err := c.Call("Arith.PtrArg", &Args{A: 5, B: 9}, &reply); err != nil {
		t.Fatalf("PtrArg: %v", err)
	}
	if reply.C != 14 {
		t.Fatalf("PtrArg = %d, want 14", reply.C)
	}

	err := c.Call("Arith.Boom", Args{}, &Reply{})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("Boom err = %v, want \"boom\"", err)
	}

	// Unknown method: stdlib client should get an error, stream stays usable.
	err = c.Call("Arith.Nope", Args{}, &Reply{})
	if err == nil {
		t.Fatalf("Nope: expected error for unknown method")
	}
	reply = Reply{}
	if err := c.Call("Arith.Multiply", Args{A: 2, B: 2}, &reply); err != nil {
		t.Fatalf("post-unknown Multiply: %v", err)
	}
	if reply.C != 4 {
		t.Fatalf("post-unknown Multiply = %d, want 4", reply.C)
	}
}

// TestGobimplClient_AsyncGo exercises the async Go() + Done channel path and
// concurrent in-flight calls against a gobimpl server.
func TestGobimplClient_AsyncGo(t *testing.T) {
	srvConn, cliConn := pipePair()
	srv := gobimpl.NewServer()
	if err := srv.Register(Arith{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	go srv.ServeConn(srvConn)

	c := gobimpl.NewClient(cliConn)
	defer c.Close() //nolint:errcheck

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reply := &Reply{}
			call := <-c.Go("Arith.Multiply", Args{A: i, B: 2}, reply, nil).Done
			if call.Error != nil {
				t.Errorf("call %d: %v", i, call.Error)
				return
			}
			if reply.C != i*2 {
				t.Errorf("call %d = %d, want %d", i, reply.C, i*2)
			}
		}(i)
	}
	wg.Wait()
}

// TestRegisterName_Accept_Dial exercises the net/rpc-Dial/RegisterName/Accept
// surface over a real TCP listener — both gobimpl↔gobimpl and stdlib↔gobimpl —
// the path appevent's event channel uses.
func TestRegisterName_Accept_Dial(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer lis.Close() //nolint:errcheck

	srv := gobimpl.NewServer()
	// RegisterName publishes under an explicit service name (not the type name).
	if err := srv.RegisterName("Calc", Arith{}); err != nil {
		t.Fatalf("RegisterName: %v", err)
	}
	go srv.Accept(lis)

	addr := lis.Addr().String()

	// gobimpl client over Dial.
	c, err := gobimpl.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("gobimpl.Dial: %v", err)
	}
	defer c.Close() //nolint:errcheck
	var reply Reply
	if err := c.Call("Calc.Multiply", Args{A: 9, B: 9}, &reply); err != nil {
		t.Fatalf("Multiply: %v", err)
	}
	if reply.C != 81 {
		t.Fatalf("Multiply = %d, want 81", reply.C)
	}

	// stdlib net/rpc client against the same gobimpl Accept server (wire-compat).
	sc, err := rpc.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("rpc.Dial: %v", err)
	}
	defer sc.Close() //nolint:errcheck
	reply = Reply{}
	if err := sc.Call("Calc.Multiply", Args{A: 7, B: 6}, &reply); err != nil {
		t.Fatalf("stdlib Multiply: %v", err)
	}
	if reply.C != 42 {
		t.Fatalf("stdlib Multiply = %d, want 42", reply.C)
	}
}

// TestClient_CloseUnblocksPending verifies Close terminates an in-flight call
// (the cancellation pattern the router relies on: select on ctx.Done then Close).
func TestClient_CloseUnblocksPending(t *testing.T) {
	srvConn, cliConn := pipePair()
	// Drain requests so the client's send() completes (net.Pipe is synchronous),
	// but never write a response — the call stays pending until Close.
	go io.Copy(io.Discard, srvConn) //nolint:errcheck

	c := gobimpl.NewClient(cliConn)
	call := c.Go("Arith.Multiply", Args{A: 1, B: 1}, &Reply{}, nil)

	// Nothing answers; close and confirm the pending call is released.
	time.AfterFunc(50*time.Millisecond, func() { c.Close() }) //nolint:errcheck,gosec

	select {
	case <-call.Done:
		if call.Error == nil {
			t.Fatalf("expected an error on the closed call")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Close did not unblock the pending call")
	}
}
