// Package plot reads sequences of numbers and draws them as line graphs.
//
// It is a Go port of https://github.com/annacrombie/plot (MIT, Stone Tickle).
// This file is the data-processing half: the transformations that run over the
// numbers before anything is drawn, which is what separates plot from a plain
// graph renderer. A monotonic counter — bytes on an interface, say — is a ramp
// until Rate turns it into a rate.
//
// The processors are streaming by construction. Each one is handed whatever
// samples have arrived so far, emits what it can, and reports how many it
// consumed; the rest stays buffered for the next read. That is what lets the
// same code serve a file read once and a counter polled forever.
package plot

import "fmt"

// Proc transforms a stream of samples.
//
// Process appends its output to dst and returns the extended slice along with
// the number of samples it consumed from the front of in. Samples left
// unconsumed are offered again, with more appended, on the next call — so a
// processor that needs a longer window than it has been given may consume
// nothing and emit nothing, and that is not an error.
//
// Implementations hold their own state, so a Proc belongs to one stream and
// cannot be shared between two.
type Proc interface {
	Process(dst, in []float64) (out []float64, consumed int)
}

// Flusher is a Proc that may still be holding samples it would only emit once
// more arrived. Flush is called when the stream has ended and no more will:
// whatever is held is all there will ever be, so it is emitted rather than
// dropped. in is the samples the processor left unconsumed.
type Flusher interface {
	Flush(dst, in []float64) []float64
}

// Block averages every n samples into one, which is the plain decimation used
// to make a long series fit a short plot.
//
// Mid-stream it emits a block only once a sample beyond that block has arrived,
// as the original does — its loop runs while i+n < len rather than <=. At the
// end of the stream the samples still held are emitted by Flush, so nothing is
// lost; in the original they are dropped, which costs a series of ten with
// avg:5 half its output.
type Block struct{ n int }

// NewBlock returns a Block averaging every n samples.
func NewBlock(n int) (*Block, error) {
	if n <= 0 || n >= BufferSize {
		return nil, fmt.Errorf("plot: block size %d out of range (1..%d)", n, BufferSize-1)
	}
	return &Block{n: n}, nil
}

func (b *Block) Process(dst, in []float64) ([]float64, int) {
	i := 0
	for i+b.n < len(in) {
		sum := 0.0
		for j := 0; j < b.n; j++ {
			sum += in[i+j]
		}
		dst = append(dst, sum/float64(b.n))
		i += b.n
	}
	return dst, i
}

// Flush emits the blocks still held at the end of the stream, including a last
// one shorter than n — an average of four samples where five were asked for is
// still what those samples say, and dropping it would lose the end of the data.
func (b *Block) Flush(dst, in []float64) []float64 {
	for i := 0; i < len(in); i += b.n {
		end := i + b.n
		if end > len(in) {
			end = len(in)
		}
		sum := 0.0
		for _, v := range in[i:end] {
			sum += v
		}
		dst = append(dst, sum/float64(end-i))
	}
	return dst
}

// Simple is a simple moving average over n samples — a boxcar filter, for
// taking the noise out of a signal without moving its features along the axis.
//
// n must be odd, so that the window has a middle sample to be centered on.
//
// While the window is still filling it emits the average of what it has, but
// only once it holds more than half a window. Before that the average would say
// more about how recently the stream started than about the signal.
//
// This differs from the original deliberately. C plot divides the warm-up sum
// by one less than the number of samples in it, so a constant signal comes out
// as a spike that decays into the true value — feeding it a flat series of 10
// with a window of 5 gives 15, 13.33, 12.5, 10. Here the divisor is the count,
// so a flat signal is flat. The emission schedule is unchanged: output still
// begins once the window is more than half full.
type Simple struct {
	n    int
	hist []float64 // the window, oldest first, never longer than n
	sum  float64   // sum of hist
	seen int       // how many samples have arrived, for the warm-up schedule
}

// NewSimple returns a Simple moving average over n samples. n must be odd.
func NewSimple(n int) (*Simple, error) {
	if n <= 0 || n >= BufferSize {
		return nil, fmt.Errorf("plot: window %d out of range (1..%d)", n, BufferSize-1)
	}
	if n%2 == 0 {
		return nil, fmt.Errorf("plot: window %d must be odd, so it has a middle to center on", n)
	}
	return &Simple{n: n}, nil
}

// Process consumes everything it is given, keeping the window in its own state
// rather than in the caller's buffer.
//
// The original keeps a running sum but leaves the samples for the caller to
// re-offer, which double-counts them if a window spans more than one read. It
// does not show up there because a read usually fills the whole buffer, so the
// window fills in one go — but it makes the result depend on how the input
// happened to be chopped up, which is exactly what a stream cannot promise.
func (s *Simple) Process(dst, in []float64) ([]float64, int) {
	for _, v := range in {
		s.hist = append(s.hist, v)
		s.sum += v
		if len(s.hist) > s.n {
			s.sum -= s.hist[0]
			// Copied down rather than resliced: reslicing would leave the
			// backing array growing for as long as the stream runs.
			s.hist = append(s.hist[:0], s.hist[1:]...)
		}
		// Output starts once the window is more than half full, which is the
		// schedule the original uses.
		if s.seen >= s.n/2 {
			dst = append(dst, s.sum/float64(len(s.hist)))
		}
		s.seen++
	}
	return dst, len(in)
}

// Cumulative is the running average of everything seen so far. Unlike Simple it
// never forgets, so it settles rather than tracks.
type Cumulative struct {
	avg float64
	n   float64
}

// NewCumulative returns a Cumulative moving average.
func NewCumulative() *Cumulative { return &Cumulative{} }

func (c *Cumulative) Process(dst, in []float64) ([]float64, int) {
	for _, v := range in {
		c.avg = (v + c.n*c.avg) / (c.n + 1)
		c.n++
		dst = append(dst, c.avg)
	}
	return dst, len(in)
}

// Rate reports how fast a value is changing rather than what it is: each sample
// becomes (value - previous) / interval.
//
// This is the one that makes a counter readable. Interface byte counts, uptime,
// anything monotonic, plotted raw, is a line going up and to the right and says
// nothing about the traffic; through Rate it is throughput.
//
// The first sample has nothing to compare against, so it comes out as zero
// rather than as a spike from nowhere.
type Rate struct {
	interval float64
	prev     float64
	started  bool
}

// NewRate returns a Rate over the given interval, which is the time between
// samples in whatever unit the caller wants the rate expressed in.
func NewRate(interval float64) (*Rate, error) {
	if interval == 0 {
		return nil, fmt.Errorf("plot: rate interval cannot be zero")
	}
	return &Rate{interval: interval}, nil
}

func (r *Rate) Process(dst, in []float64) ([]float64, int) {
	if len(in) == 0 {
		return dst, 0
	}
	if !r.started {
		r.prev = in[0]
		r.started = true
	}
	for _, v := range in {
		dst = append(dst, (v-r.prev)/r.interval)
		r.prev = v
	}
	return dst, len(in)
}
