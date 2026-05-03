// Package logstore logstore.go
package logstore

import (
	"sync"

	"github.com/sirupsen/logrus"
)

// LogRealLineKey is a key in the log entry that denotes real log line number
// in the total log (not limited by capacity of runtime log store)
const LogRealLineKey = "log_line"

// Store is in-memory log store that returns all logs as a single string
type Store interface {
	// GetLogs returns stored logs and the number of log entries overwritten
	// due to insufficient capacity.
	// returned number n means that n log entries have been dropped and the oldest
	// log entry is (n+1)th
	GetLogs() ([]string, int64)
	// GetLogsSince returns stored entries whose log_line is strictly
	// greater than since. The returned `dropped` is the number of
	// entries the caller missed because they aged out of the ring
	// buffer between calls (0 when the caller is keeping up). The
	// returned `latest` is the highest log_line currently in the
	// store — callers should pass it as `since` on the next call to
	// receive only newly-arrived entries (diff streaming).
	//
	// since == 0 returns the entire buffer (equivalent to GetLogs).
	// since >= latest returns an empty entry slice.
	GetLogsSince(since int64) (entries []string, dropped int64, latest int64)
}

// MakeStore returns a new store that will hold up to max entries,
// overwriting the oldest entry when over the capacity
// returned hook should be registered in logrus master logger to
// store log entries
func MakeStore(maxx int) (Store, logrus.Hook) {
	entries := make([]string, maxx)
	formatter := &logrus.JSONFormatter{}
	store := &store{cap: int64(maxx), entries: entries, formatter: formatter}
	return store, store
}

type store struct {
	mu sync.RWMutex
	// max number of entries to hold simultaneously
	cap int64
	// number of the next entry to come (also number of entries processed since the beginning)
	entryNum  int64
	entries   []string
	formatter logrus.Formatter
}

// collect log lines into a single string, starting at from (inclusive)
// and ending at to (not inclusive)
func (s *store) collectLogs(from, to int64) []string {
	logs := make([]string, 0)
	for i := from; i < to; i++ {
		logs = append(logs, s.entries[i])
	}
	return logs
}

// GetLogs returns most recent log lines (up to cap log lines is stored
func (s *store) GetLogs() ([]string, int64) {
	if s.entryNum < s.cap {
		return s.collectLogs(0, s.entryNum), 0
	}
	idx := s.entryNum % s.cap
	logs := s.collectLogs(idx, s.cap)
	logs = append(logs, s.collectLogs(0, idx)...)
	return logs, s.entryNum - s.cap
}

// GetLogsSince returns entries whose log_line > since. See the
// Store interface comment for `dropped`/`latest` semantics.
//
// log_line is 1-indexed; line N is stored at index (N-1) % cap.
// The oldest still-buffered line when the buffer has wrapped is
// entryNum - cap + 1; older requests yield a non-zero `dropped`
// and start from the oldest available line.
func (s *store) GetLogsSince(since int64) ([]string, int64, int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if since < 0 {
		since = 0
	}
	if since >= s.entryNum {
		return nil, 0, s.entryNum
	}

	var oldestAvailable int64 = 1
	if s.entryNum > s.cap {
		oldestAvailable = s.entryNum - s.cap + 1
	}

	startLine := since + 1
	var dropped int64
	if startLine < oldestAvailable {
		dropped = oldestAvailable - startLine
		startLine = oldestAvailable
	}

	logs := make([]string, 0, s.entryNum-startLine+1)
	for line := startLine; line <= s.entryNum; line++ {
		idx := (line - 1) % s.cap
		logs = append(logs, s.entries[idx])
	}
	return logs, dropped, s.entryNum
}

// Levels implements logrus.Hook interface. It denotes log levels
// that we are interested in
func (s *store) Levels() []logrus.Level {
	return logrus.AllLevels
}

// Fire implements logrus.Hook interface to process new log entry
// that we simply store
func (s *store) Fire(entry *logrus.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.entryNum % s.cap
	e := entry.WithField(LogRealLineKey, s.entryNum+1)
	e.Level = entry.Level
	e.Message = entry.Message
	bs, err := s.formatter.Format(e)
	if err != nil {
		return err
	}
	s.entries[idx] = string(bs)
	s.entryNum++
	return nil
}
