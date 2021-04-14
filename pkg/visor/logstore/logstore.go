package logstore

import (
	"strings"

	"github.com/sirupsen/logrus"
)

// Store is in-memory log store that returns all logs as a single string
type Store interface {
	GetLogs() (string, error)
}

// MakeStore returns a new store that will hold up to max entries,
// overwriting the oldest entry when over the capacity
// returned hook should be registered in logrus master logger to
// store log entries
func MakeStore(max int) (Store, logrus.Hook) {
	entries := make([]*logrus.Entry, max)
	store := &store{max: int64(max), entries: entries}
	return store, store
}

type store struct {
	max     int64
	n       int64
	entries []*logrus.Entry
}

// GetLogs returns most resent log lines (up to max log lines is stored)
func (s *store) GetLogs() (string, error) {
	if s.n < s.max {
		return s.collectLogs(0, s.n)
	}
	logsWrap, err := s.collectLogs(s.n, s.max)
	if err != nil {
		return "", err
	}
	logs, err := s.collectLogs(0, s.n)
	if err != nil {
		return "", err
	}
	return logsWrap + logs, nil
}

// collect log lines into a single string, starting at from (inclusive)
// and ending at to (not inclusive)
func (s *store) collectLogs(from, to int64) (string, error) {
	sb := strings.Builder{}
	for i := from; i < to; i++ {
		line, err := s.entries[i].String()
		if err != nil {
			return "", err
		}
		sb.WriteString(line)
	}
	return sb.String(), nil
}

func (s *store) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (s *store) Fire(entry *logrus.Entry) error {
	idx := s.n % s.max
	s.entries[idx] = entry
	s.n++
	return nil
}
