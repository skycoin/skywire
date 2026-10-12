package mismatch

type M struct{}

func (m *M) Both(a int, r *int) error { return nil }
