package logstore

import (
	"strings"

	"github.com/sirupsen/logrus"
)

// LogRealLineKey is a key in the log enry that denote real log line number
// in the total log (not limited by capacity of runtime log store)
const LogRealLineKey = "real_line"

// Store is in-memory log store that returns all logs as a single string
type Store interface {
	// get logs returns stored logs and the number of log entries overwritten
	// due to insufficient capacity.
	// returned number n means that n log entries have been dropped and the oldest
	// log entry is (n+1)th
	GetLogs() ([]*logrus.Entry, int64)
	// get logs as a single string
	GetLogStr() (string, error)
}

// MakeStore returns a new store that will hold up to max entries,
// overwriting the oldest entry when over the capacity
// returned hook should be registered in logrus master logger to
// store log entries
func MakeStore(max int) (Store, logrus.Hook) {
	entries := make([]*logrus.Entry, max)
	formatter := &logrus.JSONFormatter{}
	store := &store{max: int64(max), entries: entries, formatter: formatter}
	return store, store
}

type store struct {
	max       int64
	n         int64
	entries   []*logrus.Entry
	formatter logrus.Formatter
}

// GetLogs returns most resent log lines (up to max log lines is stored)
func (s *store) GetLogs() ([]*logrus.Entry, int64) {
	if s.n < s.max {
		return s.collectLogs(0, s.n), 0
	}
	idx := s.n % s.max
	logs := s.collectLogs(idx, s.max)
	logs = append(logs, s.collectLogs(0, idx)...)
	return logs, s.n - s.max
}

// collect log lines into a single string, starting at from (inclusive)
// and ending at to (not inclusive)
func (s *store) collectLogs(from, to int64) []*logrus.Entry {
	logs := make([]*logrus.Entry, 0)
	for i := from; i < to; i++ {
		logs = append(logs, s.entries[i])
	}
	return logs
}

func (s *store) GetLogStr() (string, error) {
	logs, _ := s.GetLogs()
	var sb strings.Builder
	for _, entry := range logs {
		bs, err := s.formatter.Format(entry)
		if err != nil {
			return "", err
		}
		sb.WriteString(string(bs))
	}
	return sb.String(), nil
}

func (s *store) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (s *store) Fire(entry *logrus.Entry) error {
	idx := s.n % s.max
	e := entry.WithField(LogRealLineKey, s.n)
	s.entries[idx] = e
	s.n++
	return nil
}
