package sample

import (
	"time"

	u "net/url"
)

type S struct{ x int }

type hidden struct{}

func (s *S) PtrArg(a *time.Duration, r *u.URL) error   { return nil }
func (s S) ValArg(a []string, r *map[string]int) error { return nil }
func (s *S) Grouped(_, _ *struct{}) error              { return nil }
func (s *S) unexported(a int, r *int) error            { return nil }
func (s *S) ThreeArgs(a, b int, r *int) error          { return nil }
func (s *S) NonPtrReply(a int, r int) error            { return nil }
func (s *S) NoError(a int, r *int) bool                { return false }
func (s *S) TwoResults(a int, r *int) (int, error)     { return 0, nil }
func (s *S) HiddenArg(a hidden, r *int) error          { return nil }
func (s *S) HiddenReply(a int, r *hidden) error        { return nil }
func (s *S) Variadic(a int, r ...*int) error           { return nil }
