//go:build !tinygo

package sample

func (s *S) NativeOnly(a int, r *int) error { return nil }
