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
}

// MakeStore returns a new store that will hold up to max entries,
// overwriting the oldest entry when over the capacity
// returned hook should be registered in logrus master logger to
// store log entries
func MakeStore(max int) (Store, logrus.Hook) {
	entries := make([]string, max)
	formatter := &logrus.JSONFormatter{}
	store := &store{cap: int64(max), entries: entries, formatter: formatter}
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
