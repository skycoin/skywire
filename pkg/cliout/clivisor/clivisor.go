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
	// Event marks the last record of the stream; see BandwidthProgress.
	Event         string  `json:"event,omitempty"`
	Carrier       string  `json:"carrier,omitempty"`
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

// BandwidthProgress is one sample of a bandwidth test in flight.
//
// These stream: a long run emits one JSON object per line (NDJSON) followed by
// the Bandwidth summary, matching what `visor ping mux-bw` and
// `visor ping tree-stream` already do. Suppressing them in JSON mode would
// leave a caller watching a multi-minute test with nothing to read until the
// end, while the terminal user sees it tick — the wrong way round, since the
// caller is the one that cannot see a progress line rewrite itself.
type BandwidthProgress struct {
	// Event names the record so a reader consuming the stream can tell a
	// sample from the final summary without guessing by field presence.
	Event        string  `json:"event"`
	Seconds      float64 `json:"seconds"`
	UploadKBps   float64 `json:"upload_kbps"`
	DownloadKBps float64 `json:"download_kbps"`
}

// Human writes the in-place progress line the terminal shows.
func (p BandwidthProgress) Human(w io.Writer) error {
	_, err := fmt.Fprintf(w, "\rProgress: %.1fs | Up: %.2f KB/s | Down: %.2f KB/s",
		p.Seconds, p.UploadKBps, p.DownloadKBps)
	return err
}
