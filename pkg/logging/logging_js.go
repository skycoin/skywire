//go:build js

// Package logging pkg/logging/logging_js.go
//
// WASM-clean stub for pkg/logging. The full implementation uses
// sirupsen/logrus, which transitively imports encoding/json — and
// encoding/json drags reflect runtime helpers (reflect.unsafe_New,
// mapassign, typedmemmove, etc.) that TinyGo's stdlib doesn't ship.
//
// This stub provides JUST the type + constructor surface that
// WASM-clean consumers of pkg/logging reach (visorconfig.Common,
// dmsg/disc, etc.). The methods on MasterLogger / Logger are no-ops
// — under js/wasm there's no useful log sink anyway, and the live
// install-page WASM path is small enough that nothing actually
// logs at runtime.
//
// Consumers that need real logging (the visor binary, AUR tooling,
// servers) are non-WASM by nature and use the full !js
// implementation in broadcaster.go, formatter.go, hooks.go,
// logger.go, logging.go.
package logging

import (
	"io"
)

// MasterLogger is a stub of the real MasterLogger type. The struct
// has no fields under js so consumers that hold a *MasterLogger
// (e.g. visorconfig.Common.log) compile without dragging logrus.
type MasterLogger struct{}

// Logger is the per-module logger stub. Same shape as MasterLogger
// — no fields, no behavior. The methods that the native package
// exposes (WithError, WithField, Debug, Info, Warn, Error, ...)
// aren't included because no WASM-reachable code path calls them.
// If a future WASM consumer needs to log, add the methods here as
// no-ops.
type Logger struct{}

// NewMasterLogger returns a zero-valued *MasterLogger. Mirrors the
// !js signature so visorconfig.NewCommon's default-logger branch
// works identically across builds.
func NewMasterLogger() *MasterLogger {
	return &MasterLogger{}
}

// PackageLogger returns a *Logger stub. Mirrors the native method's
// signature; the moduleName argument is ignored under js.
func (m *MasterLogger) PackageLogger(_ string) *Logger {
	return &Logger{}
}

// MustGetLogger returns a *Logger stub. Some packages call
// logging.MustGetLogger(...) at construction time even though the
// returned logger never gets exercised under js.
func MustGetLogger(_ string) *Logger {
	return &Logger{}
}

// SetOutputTo is a no-op stub. Some callers configure the package-
// level logger output; under js there's no output anyway.
func SetOutputTo(_ io.Writer) {}
