// Package clivisor is the output shape of the `skywire cli visor` commands.
package clivisor

import (
	"fmt"
	"io"
)

// Bandwidth is the result of a bandwidth test: the totals a caller wants,
// rather than the six formatted lines the command printed.
//
// Both units are given for the speeds. The human rendering showed KB/s and
// Mbps side by side, and dropping either would make a caller recompute a
// number it had already been shown.
type Bandwidth struct {
	Carrier       string  `json:"carrier"`
	Target        string  `json:"target"`
	Seconds       float64 `json:"seconds"`
	BytesSent     uint64  `json:"bytes_sent"`
	BytesReceived uint64  `json:"bytes_received"`
	UploadKBps    float64 `json:"upload_kbps"`
	DownloadKBps  float64 `json:"download_kbps"`
	UploadMbps    float64 `json:"upload_mbps"`
	DownloadMbps  float64 `json:"download_mbps"`
}

// Human writes the final-results block the command printed.
func (b Bandwidth) Human(w io.Writer) error {
	_, err := fmt.Fprintf(w, "\n=== Final Results ===\n"+
		"Duration: %.2f seconds\n"+
		"Bytes Sent: %d (%.2f MB)\n"+
		"Bytes Received: %d (%.2f MB)\n"+
		"Upload Speed: %.2f KB/s (%.2f Mbps)\n"+
		"Download Speed: %.2f KB/s (%.2f Mbps)\n",
		b.Seconds,
		b.BytesSent, float64(b.BytesSent)/1024/1024,
		b.BytesReceived, float64(b.BytesReceived)/1024/1024,
		b.UploadKBps, b.UploadMbps,
		b.DownloadKBps, b.DownloadMbps)
	return err
}

// Goroutines is a visor's goroutine census.
//
// Dump is the raw runtime stack text, carried as one field rather than parsed
// into structure: it is Go's own format, a caller wanting it wants it verbatim,
// and inventing a schema over it would be a second thing to keep correct.
type Goroutines struct {
	Total   int    `json:"total"`
	Summary string `json:"summary,omitempty"`
	Dump    string `json:"dump,omitempty"`
}

// Human writes whichever rendering was asked for.
func (g Goroutines) Human(w io.Writer) error {
	if g.Dump != "" {
		_, err := io.WriteString(w, g.Dump)
		return err
	}
	_, err := fmt.Fprintln(w, g.Summary)
	return err
}
