// Package appcommon pkg/app/appcommon/log_store.go
package appcommon

import (
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/logging"
)

const (
	timeLayout = time.RFC3339Nano
)

// NewProcLogger returns a new proc logger.
// It creates an isolated MasterLogger for the app to prevent log cross-contamination
// between multiple apps that would occur if they shared the same logger with multiple hooks.
//
// The backing LogStore is selected per build by newProcLogStore: a bbolt store
// on native builds (log_store_native.go), an in-memory store on TinyGo
// (log_store_tinygo.go), where bbolt does not compile.
func NewProcLogger(conf ProcConfig, mLog *logging.MasterLogger) (appLog *logging.MasterLogger, db LogStore) {
	store, err := newProcLogStore(conf)
	if err != nil {
		panic(err)
	}
	db = store

	// Create isolated MasterLogger for this app instead of reusing shared one.
	// This prevents hooks from one app affecting logs from other apps —
	// specifically the per-app LogStore. We copy the essential settings (Out,
	// Formatter, Level) from the parent logger to ensure consistent behavior,
	// and re-attach the parent's existing hooks (e.g. the runtime logstore + the
	// gRPC log Broadcaster used by 'cli proxy start --verbose') so cross-cutting
	// observers see every app's logs. The per-app store is then layered on top —
	// it's still scoped to this AppName, so isolation at the storage layer is
	// preserved.
	appLog = logging.NewMasterLogger()
	appLog.SetLevel(mLog.GetLevel())
	appLog.Logger.Out = mLog.Logger.Out
	if mLog.Logger.Formatter != nil {
		appLog.Logger.Formatter = mLog.Logger.Formatter
	}
	for _, hooks := range mLog.Logger.Hooks {
		for _, h := range hooks {
			appLog.Logger.AddHook(h)
		}
	}
	appLog.Logger.AddHook(db)

	return appLog, db
}

// TimestampFromLog is an utility function for retrieving the timestamp from a log. This function should be modified
// if the time layout is changed
func TimestampFromLog(logLine string) string {
	return strings.Split(logLine[1:1+len(timeLayout)], "]")[0]
}

// LogStore stores logs from apps, for later consumption from the hypervisor
type LogStore interface {
	// Write implements io.Writer
	Write(p []byte) (n int, err error)

	// Store saves given log in db
	Store(t time.Time, s string) error

	// LogSince returns the logs since given timestamp. For optimal performance,
	// the timestamp should exist in the store (you can get it from previous logs),
	// otherwise the DB will be sequentially iterated until finding entries older than given timestamp
	LogsSince(t time.Time) ([]string, error)

	Fire(entry *log.Entry) error
	Levels() []log.Level
	Flush() error
}
