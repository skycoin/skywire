// Package noop noop_test.go: unit tests for the noop service factory and
// its Run loop (default tick, explicit tick that fires, and the
// negative "no-tick" placeholder mode).
package noop

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
)

func testLogger() *logging.Logger {
	return logging.NewMasterLogger().PackageLogger("noop-test")
}

func TestType(t *testing.T) {
	require.Equal(t, "noop", Type)
}

func TestRegistered(t *testing.T) {
	// init() registers the factory under Type; it must show up in the
	// shared registry.
	require.Contains(t, services.RegisteredTypes(), Type)
}

func TestFactory_ValidConfig(t *testing.T) {
	raw := json.RawMessage(`{"type":"noop","name":"demo","tick_seconds":5}`)
	svc, err := factory(raw, testLogger())
	require.NoError(t, err)
	require.NotNil(t, svc)

	s, ok := svc.(*service)
	require.True(t, ok)
	require.Equal(t, "noop", s.cfg.Type)
	require.Equal(t, "demo", s.cfg.Name)
	require.Equal(t, 5, s.cfg.TickSeconds)
}

func TestFactory_InvalidJSON(t *testing.T) {
	svc, err := factory(json.RawMessage(`{not valid`), testLogger())
	require.Error(t, err)
	require.Nil(t, svc)
	require.Contains(t, err.Error(), "noop: parse config")
}

func TestRun_DefaultTickCancels(t *testing.T) {
	// TickSeconds == 0 falls back to the 30s default; canceling ctx
	// must unwind the loop promptly and return nil (without waiting a
	// full interval).
	s := &service{cfg: Config{Type: "noop"}, log: testLogger()}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

func TestRun_TickFires(t *testing.T) {
	// A 1s tick exercises the ticker branch (the "tick" log) before we
	// cancel.
	s := &service{cfg: Config{Type: "noop", TickSeconds: 1}, log: testLogger()}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	time.Sleep(1200 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

func TestRun_NegativeTickNoTick(t *testing.T) {
	// Negative TickSeconds disables ticking: Run blocks on ctx and
	// returns nil once canceled.
	s := &service{cfg: Config{Type: "noop", TickSeconds: -1}, log: testLogger()}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}
