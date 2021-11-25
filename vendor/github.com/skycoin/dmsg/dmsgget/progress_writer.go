package dmsgget

import (
	"fmt"
	"sync/atomic"
)

// ProgressWriter prints the progress of a download to stdout.
type ProgressWriter struct {
	// atomic requires 64-bit alignment for struct field access
	Current int64
	Total   int64
}

// Write implements io.Writer
func (pw *ProgressWriter) Write(p []byte) (int, error) {
	n := len(p)

	current := atomic.AddInt64(&pw.Current, int64(n))
	total := atomic.LoadInt64(&pw.Total)
	pc := fmt.Sprintf("%d%%", current*100/total)

	fmt.Printf("Downloading: %d/%dB (%s)", current, total, pc)
	if current != total {
		fmt.Print("\r")
	} else {
		fmt.Print("\n")
	}

	return n, nil
}
