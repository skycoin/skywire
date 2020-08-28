package updater

import (
	"strings"
	"sync/atomic"
)

type status struct {
	atomic.Value
}

func newStatus() *status {
	return &status{}
}

func (s *status) Get() string {
	if v, ok := s.Value.Load().(string); ok {
		return v
	}

	return ""
}

func (s *status) Set(v string) {
	s.Value.Store(v)
}

func (s *status) Write(p []byte) (n int, err error) {
	s.Set(strings.TrimSpace(string(p)))
	return len(p), nil
}
