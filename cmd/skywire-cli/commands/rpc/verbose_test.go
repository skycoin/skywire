// Package clirpc verbose_test.go: unit tests for the verbose log-stream
// helpers — emitEntry formatting, the OpenVerbose/WithVerbose filter guards,
// and the WaitSubscribed select arms — without a live gRPC server.
package clirpc

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

func canceledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// ---- emitEntry -------------------------------------------------------------

func TestEmitEntry(t *testing.T) {
	var buf bytes.Buffer
	emitEntry(&buf, &rpcgrpc.AppLogEntry{
		TimestampNs: time.Now().UnixNano(),
		Level:       "info",
		Module:      "router",
		Message:     "hello world",
		Fields:      map[string]string{"key": "val"},
	})
	out := buf.String()
	require.Contains(t, out, "hello world")
	require.Contains(t, out, "router")
	require.Contains(t, out, "key")
}

func TestEmitEntry_UnknownLevelFallsBackToDebug(t *testing.T) {
	var buf bytes.Buffer
	emitEntry(&buf, &rpcgrpc.AppLogEntry{
		TimestampNs: time.Now().UnixNano(),
		Level:       "not-a-level",
		Message:     "msg",
	})
	require.Contains(t, buf.String(), "msg")
}

// ---- OpenVerbose -----------------------------------------------------------

func TestOpenVerbose_EmptyFilterErrors(t *testing.T) {
	_, err := OpenVerbose(context.Background(), "127.0.0.1:1", VerboseFilter{})
	require.Error(t, err)
}

func TestOpenVerbose_ValidFilterCanceledCtx(t *testing.T) {
	// gRPC NewClient is lazy, so OpenVerbose succeeds even against a dead
	// address; the streaming goroutine fails fast on the canceled context
	// and Close unwinds it.
	v, err := OpenVerbose(canceledCtx(), "127.0.0.1:1", VerboseFilter{AppName: "vpn"})
	require.NoError(t, err)
	require.NotNil(t, v)

	done := make(chan struct{})
	go func() { v.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("VerboseStream.Close did not return")
	}
}

// ---- WaitSubscribed --------------------------------------------------------

func TestWaitSubscribed_Subscribed(t *testing.T) {
	v := &VerboseStream{subscribed: make(chan struct{}), done: make(chan struct{})}
	close(v.subscribed)
	require.NoError(t, v.WaitSubscribed(context.Background(), time.Second))
}

func TestWaitSubscribed_Timeout(t *testing.T) {
	v := &VerboseStream{subscribed: make(chan struct{}), done: make(chan struct{})}
	err := v.WaitSubscribed(context.Background(), time.Millisecond)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestWaitSubscribed_CtxCanceled(t *testing.T) {
	v := &VerboseStream{subscribed: make(chan struct{}), done: make(chan struct{})}
	err := v.WaitSubscribed(canceledCtx(), time.Second)
	require.Error(t, err)
}

// ---- WithVerbose -----------------------------------------------------------

func TestWithVerbose_EmptyFilterRunsFn(t *testing.T) {
	sentinel := errors.New("ran")
	err := WithVerbose(context.Background(), "127.0.0.1:1", VerboseFilter{}, func() error {
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)
}

func TestWithVerbose_WithFilterRunsFn(t *testing.T) {
	// Canceled ctx makes WaitSubscribed return promptly; fn still runs and
	// its result propagates.
	ran := false
	err := WithVerbose(canceledCtx(), "127.0.0.1:1", VerboseFilter{Modules: []string{"dmsg"}}, func() error {
		ran = true
		return nil
	})
	require.NoError(t, err)
	require.True(t, ran)
}
