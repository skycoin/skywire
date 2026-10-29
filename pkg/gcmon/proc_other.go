//go:build !linux

package gcmon

func readProc(_ *[]byte) (rss, threads uint64) { return 0, 0 }
