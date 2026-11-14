//go:build !linux

// Package gcmon pkg/gcmon/proc_other.go c0-com-util
package gcmon

func readProc(_ *[]byte) (rss, threads uint64) { return 0, 0 }
