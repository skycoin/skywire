//go:build tinygo || js

// Package appcommon pkg/app/appcommon/log_store_tinygo.go c2-vis-appsvc
//
// In-memory LogStore for the TinyGo js/wasm target, where bbolt (the native
// backing store in log_store_native.go) does not compile. App log history is
// local telemetry; a browser visor keeps a bounded ring in memory. The shared
// interface + NewProcLogger are in log_store.go.
package appcommon

import (
	"io"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// maxMemLogs bounds the in-memory ring so a long-lived browser app can't grow
// its log buffer without limit. Oldest entries are dropped first.
const maxMemLogs = 4096

// newProcLogStore is the TinyGo (in-memory) backing store for NewProcLogger.
func newProcLogStore(_ ProcConfig) (LogStore, error) {
	return newMemLogStore(), nil
}

type memLogEntry struct {
	t time.Time
	s string
}

type memLogStore struct {
	mx   sync.RWMutex
	logs []memLogEntry
}

func newMemLogStore() *memLogStore {
	return &memLogStore{logs: make([]memLogEntry, 0, 64)}
}

func (m *memLogStore) add(t time.Time, s string) {
	m.mx.Lock()
	defer m.mx.Unlock()
	m.logs = append(m.logs, memLogEntry{t: t, s: s})
	if over := len(m.logs) - maxMemLogs; over > 0 {
		m.logs = m.logs[over:]
	}
}

// Write implements io.Writer. The log line carries an RFC3339Nano timestamp in
// bytes [1 : 1+len(timeLayout)], matching the native store's framing.
func (m *memLogStore) Write(p []byte) (int, error) {
	if len(p) < len(timeLayout)+2 {
		return 0, io.ErrShortBuffer
	}
	t, err := time.Parse(timeLayout, string(p[1:1+len(timeLayout)]))
	if err != nil {
		t = time.Time{}
	}
	m.add(t, string(p))
	return len(p), nil
}

// Store implements LogStore.
func (m *memLogStore) Store(t time.Time, s string) error {
	m.add(t, s)
	return nil
}

// LogsSince implements LogStore.
func (m *memLogStore) LogsSince(t time.Time) ([]string, error) {
	m.mx.RLock()
	defer m.mx.RUnlock()
	out := make([]string, 0)
	for _, e := range m.logs {
		if !e.t.Before(t) {
			out = append(out, e.s)
		}
	}
	return out, nil
}

// Fire implements logrus.Hook.
func (m *memLogStore) Fire(entry *log.Entry) error {
	s, err := entry.String()
	if err != nil {
		return err
	}
	// Mirror the native store's timestamp parse from the rendered line so
	// LogsSince ordering is consistent, falling back to the entry time.
	t := entry.Time
	if len(s) >= len(timeLayout)+2 {
		if parsed, perr := time.Parse(timeLayout, strings.Split(s[1:1+len(timeLayout)], "]")[0]); perr == nil {
			t = parsed
		}
	}
	m.add(t, s)
	return nil
}

// Levels implements logrus.Hook.
func (m *memLogStore) Levels() []log.Level { return log.AllLevels }

// Flush implements LogStore.
func (m *memLogStore) Flush() error { return nil }
