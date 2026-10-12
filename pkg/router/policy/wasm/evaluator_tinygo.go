//go:build tinygo

// Package wasm pkg/router/policy/wasm/evaluator_tinygo.go
//
// The TinyGo build has no WASM engine, since wazero's compiler needs Go
// assembly that TinyGo cannot link. Starlark presets carry the same names.
package wasm

import (
	"context"
	"errors"
	"time"

	"github.com/skycoin/skywire/pkg/router/policy"
)

var errNoWASM = errors.New("wasm routing policies are not available in the TinyGo build")

// Clock supplies the time to a policy.
type Clock interface{ Now() time.Time }

// Evaluator is never constructed under TinyGo.
type Evaluator struct{}

// Option configures an Evaluator.
type Option func(*Evaluator)

// WithProvider is accepted and ignored.
func WithProvider(policy.Provider) Option { return func(*Evaluator) {} }

// WithLogger is accepted and ignored.
func WithLogger(func(format string, args ...interface{})) Option { return func(*Evaluator) {} }

// WithClock is accepted and ignored.
func WithClock(Clock) Option { return func(*Evaluator) {} }

// WithPreset is accepted and ignored.
func WithPreset(string) Option { return func(*Evaluator) {} }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// NewSystemClock returns the wall clock.
func NewSystemClock() Clock { return systemClock{} }

// NewEvaluator always fails under TinyGo.
func NewEvaluator(string, []byte, ...Option) (*Evaluator, error) { return nil, errNoWASM }

// Close does nothing.
func (*Evaluator) Close() error { return nil }

// Decide always fails.
func (*Evaluator) Decide(context.Context, policy.RoutingContext, []policy.Candidate) (policy.RouteSpec, error) {
	return policy.RouteSpec{}, errNoWASM
}

// OnTick always fails.
func (*Evaluator) OnTick(context.Context, policy.RoutingContext, []policy.LegInfo) (policy.RotationAction, error) {
	return policy.RotationAction{}, errNoWASM
}

// OnLegChange always fails.
func (*Evaluator) OnLegChange(context.Context, policy.RoutingContext, []policy.LegInfo, policy.LegChange) (policy.RouteSpec, error) {
	return policy.RouteSpec{}, errNoWASM
}

// SelfHealTarget reports none.
func (*Evaluator) SelfHealTarget() (int, bool) { return 0, false }
