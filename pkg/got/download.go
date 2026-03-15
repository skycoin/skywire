package got

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Info holds downloadable file info.
type Info struct {
	Size      uint64
	Rangeable bool
}

// Download holds the configuration and state for a file download.
type Download struct {
	Client *http.Client

	// Concurrency is the number of concurrent chunk downloads. 0 = auto.
	Concurrency uint

	// URL is the download URL.
	URL string

	// Dir is the output directory.
	Dir string

	// Dest is the output filename. If empty, derived from URL or Content-Disposition.
	Dest string

	// Interval controls progress callback frequency in milliseconds. 0 = auto.
	Interval uint64

	// ChunkSize in bytes. 0 = auto.
	ChunkSize uint64

	// MinChunkSize sets the minimum chunk size in bytes.
	MinChunkSize uint64

	// MaxChunkSize sets the maximum chunk size in bytes.
	MaxChunkSize uint64

	// MaxRetries per chunk. 0 = 3 (default).
	MaxRetries int

	// Resume enables resuming a previously interrupted download using state file.
	Resume bool

	// Header contains additional HTTP headers for the request.
	Header []Header

	// StopProgress signals the progress goroutine to stop.
	StopProgress bool

	path       string
	unsafeName string
	ctx        context.Context
	size       uint64
	lastSize   uint64
	info       *Info
	chunks     []*Chunk
	startedAt  time.Time
}

// resumeState stores chunk progress for resume support.
type resumeState struct {
	URL    string   `json:"url"`
	Size   uint64   `json:"size"`
	Chunks []*Chunk `json:"chunks"`
}

// GetInfoOrDownload probes the server with a range request.
// If the server supports ranges (RFC 7233), it returns size info.
// Otherwise, it downloads the entire file in a single stream.
func (d *Download) GetInfoOrDownload() (*Info, error) {
	req, err := NewRequest(d.ctx, "GET", d.URL, append(d.Header, Header{"Range", "bytes=0-0"}), nil)
	if err != nil {
		return &Info{}, err
	}

	res, err := d.Client.Do(req)
	if err != nil {
		return &Info{}, err
	}
	defer res.Body.Close() //nolint:errcheck

	if res.StatusCode >= 300 {
		return &Info{}, fmt.Errorf("HTTP %d: %s", res.StatusCode, res.Status)
	}

	d.unsafeName = res.Header.Get("content-disposition")

	dest, err := os.Create(d.Path())
	if err != nil {
		return &Info{}, err
	}
	defer dest.Close() //nolint:errcheck

	if _, err = io.Copy(dest, io.TeeReader(res.Body, d)); err != nil {
		return &Info{}, err
	}

	// Check for content-range header indicating partial content support.
	if cr := res.Header.Get("content-range"); cr != "" && res.ContentLength == 1 {
		parts := strings.Split(cr, "/")
		if len(parts) == 2 {
			if length, err := strconv.ParseUint(parts[1], 10, 64); err == nil {
				return &Info{
					Size:      length,
					Rangeable: true,
				}, nil
			}
		}
		return &Info{}, fmt.Errorf("invalid content-range header: %s", cr)
	}

	return &Info{}, nil
}

// Init sets defaults, probes the server, and splits the file into chunks.
func (d *Download) Init() error {
	d.startedAt = time.Now()

	if d.Client == nil {
		d.Client = DefaultClient()
	}
	if d.ctx == nil {
		d.ctx = context.Background()
	}
	if d.MaxRetries == 0 {
		d.MaxRetries = 3
	}

	// Try to resume from state file
	if d.Resume {
		if state, err := d.loadState(); err == nil && state.URL == d.URL {
			d.info = &Info{Size: state.Size, Rangeable: true}
			d.chunks = state.Chunks
			// Count already downloaded bytes
			for _, c := range d.chunks {
				if c.Done {
					atomic.AddUint64(&d.size, c.End-c.Start+1)
				}
			}
			return nil
		}
	}

	var err error
	d.info, err = d.GetInfoOrDownload()
	if err != nil {
		return err
	}

	if !d.info.Rangeable {
		return nil
	}

	if d.Concurrency == 0 {
		d.Concurrency = getDefaultConcurrency()
	}

	if d.ChunkSize == 0 {
		d.ChunkSize = getDefaultChunkSize(d.info.Size, d.MinChunkSize, d.MaxChunkSize, uint64(d.Concurrency))
	}

	chunksLen := d.info.Size / d.ChunkSize
	d.chunks = make([]*Chunk, 0, chunksLen)

	for i := uint64(0); i < chunksLen; i++ {
		chunk := &Chunk{
			Start: (d.ChunkSize * i) + i,
			End:   (d.ChunkSize * i) + i + d.ChunkSize,
		}
		if chunk.End >= d.info.Size || i == chunksLen-1 {
			chunk.End = d.info.Size - 1
			d.chunks = append(d.chunks, chunk)
			break
		}
		d.chunks = append(d.chunks, chunk)
	}

	return nil
}

// Start downloads all chunks concurrently and writes them to the output file.
func (d *Download) Start() error {
	if !d.info.Rangeable {
		select {
		case <-d.ctx.Done():
			return d.ctx.Err()
		default:
			return nil
		}
	}

	file, err := os.Create(d.Path())
	if err != nil {
		return err
	}
	defer file.Close() //nolint:errcheck

	if err := file.Truncate(int64(d.TotalSize())); err != nil {
		return err
	}

	errs := make(chan error, 1)
	go d.dl(file, errs)

	select {
	case err = <-errs:
	case <-d.ctx.Done():
		err = d.ctx.Err()
	}

	// Clean up state file on success
	if err == nil {
		os.Remove(d.statePath()) //nolint:errcheck
	}

	return err
}

// RunProgress calls fn periodically until StopProgress is set.
func (d *Download) RunProgress(fn ProgressFunc) {
	if d.Interval == 0 {
		d.Interval = uint64(400 / runtime.NumCPU())
	}

	sleepd := time.Duration(d.Interval) * time.Millisecond

	for !d.StopProgress {
		select {
		case <-d.ctx.Done():
			return
		default:
		}

		fn(d)
		atomic.StoreUint64(&d.lastSize, atomic.LoadUint64(&d.size))
		time.Sleep(sleepd)
	}
}

// Context returns the download context.
func (d *Download) Context() context.Context {
	return d.ctx
}

// TotalSize returns the total file size (0 if unknown).
func (d *Download) TotalSize() uint64 {
	if d.info == nil {
		return 0
	}
	return d.info.Size
}

// Size returns the number of bytes downloaded so far.
func (d *Download) Size() uint64 {
	return atomic.LoadUint64(&d.size)
}

// Speed returns the current download speed in bytes per second.
func (d *Download) Speed() uint64 {
	interval := d.Interval
	if interval == 0 {
		interval = 400
	}
	return (atomic.LoadUint64(&d.size) - atomic.LoadUint64(&d.lastSize)) / interval * 1000
}

// AvgSpeed returns the average download speed in bytes per second.
func (d *Download) AvgSpeed() uint64 {
	if totalMs := d.TotalCost().Milliseconds(); totalMs > 0 {
		return atomic.LoadUint64(&d.size) / uint64(totalMs) * 1000
	}
	return 0
}

// TotalCost returns how long the download has been running.
func (d *Download) TotalCost() time.Duration {
	return time.Since(d.startedAt)
}

// Write implements io.Writer to track download progress.
func (d *Download) Write(b []byte) (int, error) {
	n := len(b)
	atomic.AddUint64(&d.size, uint64(n))
	return n, nil
}

// IsRangeable returns whether the server supports partial content.
func (d *Download) IsRangeable() bool {
	return d.info != nil && d.info.Rangeable
}

// Path returns the output file path.
func (d *Download) Path() string {
	if d.path == "" {
		d.path = GetFilename(d.URL)
		if d.Dest != "" {
			d.path = d.Dest
		} else if d.unsafeName != "" {
			if name := getNameFromHeader(d.unsafeName); name != "" {
				d.path = name
			}
		}
		d.path = filepath.Join(d.Dir, d.path)
	}
	return d.path
}

// dl downloads chunks concurrently, writing to dest.
func (d *Download) dl(dest io.WriterAt, errC chan error) {
	var wg sync.WaitGroup
	max := make(chan int, d.Concurrency)

	for i := 0; i < len(d.chunks); i++ {
		if d.chunks[i].Done {
			continue
		}

		max <- 1
		wg.Add(1)

		go func(idx int) {
			defer wg.Done()

			chunk := d.chunks[idx]
			if err := d.downloadChunkWithRetry(chunk, &OffsetWriter{dest, int64(chunk.Start)}); err != nil {
				errC <- err
				return
			}

			chunk.Done = true
			d.saveState() //nolint:errcheck

			<-max
		}(i)
	}

	wg.Wait()
	errC <- nil
}

// downloadChunkWithRetry downloads a chunk with exponential backoff retry.
func (d *Download) downloadChunkWithRetry(c *Chunk, dest io.Writer) error {
	var lastErr error
	for attempt := 0; attempt < d.MaxRetries; attempt++ {
		if err := d.DownloadChunk(c, dest); err != nil {
			lastErr = err
			if d.ctx.Err() != nil {
				return err
			}
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			select {
			case <-time.After(backoff):
			case <-d.ctx.Done():
				return d.ctx.Err()
			}
			continue
		}
		return nil
	}
	return fmt.Errorf("chunk %d-%d failed after %d retries: %w", c.Start, c.End, d.MaxRetries, lastErr)
}

// DownloadChunk downloads a single byte range.
func (d *Download) DownloadChunk(c *Chunk, dest io.Writer) error {
	req, err := NewRequest(d.ctx, "GET", d.URL, d.Header, nil)
	if err != nil {
		return err
	}

	contentRange := fmt.Sprintf("bytes=%d-%d", c.Start, c.End)
	req.Header.Set("Range", contentRange)

	res, err := d.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close() //nolint:errcheck

	if res.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("expected 206 Partial Content, got %d for range %s", res.StatusCode, contentRange)
	}

	expectedLen := int64(c.End - c.Start + 1)
	if res.ContentLength > 0 && res.ContentLength != expectedLen {
		return fmt.Errorf("range %s: expected Content-Length %d, got %d", contentRange, expectedLen, res.ContentLength)
	}

	_, err = io.CopyN(dest, io.TeeReader(res.Body, d), expectedLen)
	return err
}

// statePath returns the path of the resume state file.
func (d *Download) statePath() string {
	return d.Path() + ".got.json"
}

// saveState writes chunk progress to a state file for resume support.
func (d *Download) saveState() error {
	state := resumeState{
		URL:    d.URL,
		Size:   d.info.Size,
		Chunks: d.chunks,
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(d.statePath(), data, 0600)
}

// loadState reads chunk progress from a state file.
func (d *Download) loadState() (*resumeState, error) {
	data, err := os.ReadFile(d.statePath())
	if err != nil {
		return nil, err
	}
	var state resumeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// NewDownload creates a new Download with the given context, URL, and destination.
func NewDownload(ctx context.Context, URL, dest string) *Download {
	return &Download{
		ctx:    ctx,
		URL:    URL,
		Dest:   dest,
		Client: DefaultClient(),
	}
}

func getDefaultConcurrency() uint {
	c := uint(runtime.NumCPU() * 3)
	if c > 20 {
		c = 20
	}
	if c <= 2 {
		c = 4
	}
	return c
}

func getDefaultChunkSize(totalSize, min, max, concurrency uint64) uint64 {
	cs := totalSize / concurrency

	if cs >= 102400000 {
		cs = cs / 2
	}

	if min == 0 {
		min = 2097152 // 2 MB
		if min >= totalSize {
			min = totalSize / 2
		}
	}

	if cs < min {
		cs = min
	}

	if max > 0 && cs > max {
		cs = max
	}

	if cs >= totalSize {
		cs = totalSize / 2
	}

	return cs
}
