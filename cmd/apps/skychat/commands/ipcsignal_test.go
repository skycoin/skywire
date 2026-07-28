// Package commands cmd/apps/skychat/commands/ipcsignal_test.go
//
// Unit coverage for the IPC shutdown-signal handler.
//
// The load-bearing test here is TestIPCSignalLoop_ReadErrorStopsAfterOneRead.
// This loop once lacked its `break` on a read error, and golang-ipc's Read
// closes the receive channel on the first error — so every later Read returned
// instantly and the loop spun at full tilt, logging hundreds of lines per
// millisecond. On the 2-core Windows CI runner that starved the HTTP server
// and dmsg, and skychat's port stopped answering (the visor-B "connection
// refused" failures in the native e2e suite).
//
// A test that only checks "the loop exits" would pass on a build that spins a
// million times first, so the assertion is on the READ COUNT: exactly one.
// The fake keeps returning the error forever — as the real client does once
// its channel is closed — so a build without the break never leaves the loop
// on its own. Verified by mutation: deleting the break makes the fake log
// ~2.6e8 reads in ten seconds.
package commands

import (
	"errors"
	"sync"
	"testing"
	"time"

	ipc "github.com/james-barrow/golang-ipc"

	"github.com/skycoin/skywire/pkg/skyenv"
)

// fakeIPC is a scripted ipcConn. Each Read pops the next step; once the script
// is exhausted it repeats the last one forever, which is what golang-ipc does
// after it closes the receive channel — and what turns a missing `break` into
// a spin instead of a clean exit.
type fakeIPC struct {
	mu     sync.Mutex
	script []ipcStep
	reads  int
	closed int
	// maxReads bounds a runaway loop. Past it Read hands back a SHUTDOWN
	// message rather than another error: a build that ignores read errors
	// would ignore one more error too and keep spinning, whereas the shutdown
	// type breaks the loop on a path that build still honors. That converts a
	// regression from a ten-second watchdog timeout into an immediate,
	// precise read-count failure.
	maxReads int
}

type ipcStep struct {
	msg *ipc.Message
	err error
}

func newFakeIPC(script ...ipcStep) *fakeIPC {
	return &fakeIPC{script: script, maxReads: 100}
}

func (f *fakeIPC) Read() (*ipc.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.reads > f.maxReads {
		return msg(skyenv.IPCShutdownMessageType), nil // see maxReads
	}
	idx := f.reads - 1
	if idx >= len(f.script) {
		idx = len(f.script) - 1 // repeat the terminal step, like the real client
	}
	s := f.script[idx]
	return s.msg, s.err
}

func (f *fakeIPC) Close() {
	f.mu.Lock()
	f.closed++
	f.mu.Unlock()
}

func (f *fakeIPC) counts() (reads, closed int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads, f.closed
}

// runLoop runs ipcSignalLoop with a watchdog so a spinning regression fails
// loudly instead of blocking the package for the whole test timeout.
func runLoop(t *testing.T, f *fakeIPC) {
	t.Helper()
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ipcSignalLoop(f)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		reads, _ := f.counts()
		t.Fatalf("ipcSignalLoop did not return (%d reads) — the read loop is spinning", reads)
	}
}

func msg(msgType int) *ipc.Message { return &ipc.Message{MsgType: msgType} }

// TestIPCSignalLoop_ReadErrorStopsAfterOneRead is the regression guard for the
// Windows CI outage: a read error must terminate the loop immediately.
func TestIPCSignalLoop_ReadErrorStopsAfterOneRead(t *testing.T) {
	// The real failure mode: Read errors and keeps erroring, because
	// golang-ipc closed the receive channel underneath it.
	f := newFakeIPC(ipcStep{err: errors.New("the received channel has been closed")})
	runLoop(t, f)

	reads, closed := f.counts()
	if reads != 1 {
		t.Errorf("Read called %d times after an error; want exactly 1 "+
			"(more than one means the loop is spinning on a dead channel)", reads)
	}
	if closed != 1 {
		t.Errorf("Close called %d times, want 1 — the client must be released on the error path", closed)
	}
}

// TestIPCSignalLoop_ErrorAfterTrafficStillStops covers the realistic sequence:
// normal messages flow, then the connection dies mid-stream.
func TestIPCSignalLoop_ErrorAfterTrafficStillStops(t *testing.T) {
	f := newFakeIPC(
		ipcStep{msg: msg(1)},
		ipcStep{msg: msg(2)},
		ipcStep{err: errors.New("connection lost")},
	)
	runLoop(t, f)

	reads, closed := f.counts()
	if reads != 3 {
		t.Errorf("Read called %d times, want 3 (two messages then the error)", reads)
	}
	if closed != 1 {
		t.Errorf("Close called %d times, want 1", closed)
	}
}

func TestIPCSignalLoop_ShutdownMessageStopsLoop(t *testing.T) {
	f := newFakeIPC(ipcStep{msg: msg(skyenv.IPCShutdownMessageType)})
	runLoop(t, f)

	reads, closed := f.counts()
	if reads != 1 {
		t.Errorf("Read called %d times, want 1 — the shutdown message ends the loop", reads)
	}
	if closed != 1 {
		t.Errorf("Close called %d times, want 1", closed)
	}
}

// TestIPCSignalLoop_IgnoresOtherMessageTypes — only the shutdown type ends the
// handler; anything else (including golang-ipc's negative internal types, such
// as the connect/reconnect status messages it injects) is skipped.
func TestIPCSignalLoop_IgnoresOtherMessageTypes(t *testing.T) {
	f := newFakeIPC(
		ipcStep{msg: msg(-1)}, // internal status message
		ipcStep{msg: msg(1)},
		ipcStep{msg: msg(skyenv.IPCShutdownMessageType + 1)}, // adjacent, must not match
		ipcStep{msg: msg(skyenv.IPCShutdownMessageType)},
	)
	runLoop(t, f)

	if reads, _ := f.counts(); reads != 4 {
		t.Errorf("Read called %d times, want 4 — only the shutdown type should end the loop", reads)
	}
}

// TestIPCSignalLoop_NilMessageIsSkipped — Read may hand back (nil, nil); the
// loop must not deref it. Without the `if m != nil` guard this panics.
func TestIPCSignalLoop_NilMessageIsSkipped(t *testing.T) {
	f := newFakeIPC(
		ipcStep{}, // nil message, nil error
		ipcStep{msg: msg(skyenv.IPCShutdownMessageType)},
	)
	runLoop(t, f)

	if reads, _ := f.counts(); reads != 2 {
		t.Errorf("Read called %d times, want 2 — a nil message is skipped, not fatal", reads)
	}
}

// TestHandleIPCSignal_NilClient — the guard that keeps a failed StartClient
// from panicking the handler goroutine. ipcStartupDelay is zeroed so the test
// doesn't sit out the real startup grace period.
func TestHandleIPCSignal_NilClient(t *testing.T) {
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	prev := ipcStartupDelay
	ipcStartupDelay = 0
	t.Cleanup(func() { ipcStartupDelay = prev })

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleIPCSignal(nil) // must return, not deref
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleIPCSignal(nil) did not return")
	}
}
