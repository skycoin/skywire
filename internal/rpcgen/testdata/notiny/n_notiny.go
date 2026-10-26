//go:build !tinygo

package notiny

func (n *N) NativeOnly(a int, r *int) error { return nil }
