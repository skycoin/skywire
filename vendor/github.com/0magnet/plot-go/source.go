package plot

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Source is one stream of numbers: somewhere to read from, the flags that say
// how, and the pipeline the numbers go through on the way out.
type Source struct {
	// Name is what the source was opened as, for error messages.
	Name string

	// Rewind makes the source start over when it reaches the end, instead of
	// stopping there.
	//
	// This is what makes polling a counter work. A file under /sys holds one
	// number that changes; it does not grow. Reading to the end and waiting
	// for more, which is what following a log means, would wait forever — the
	// value has to be read again from the top each time.
	Rewind bool

	// Infinite keeps the source open when it reaches the end, so that data
	// appended later is picked up — following a log, as tail -f does.
	//
	// It is separate from Rewind because the two answer different questions. A
	// log grows, so following it means waiting at the end; a counter changes in
	// place, so following it means going back to the start. The original has
	// both for the same reason: -f sets this, and :r sets Rewind.
	Infinite bool

	r      io.Reader
	closer io.Closer
	seeker io.Seeker
	split  Splitter
	buf    []byte
	done   bool

	pipe *Pipeline
}

// OpenSource opens spec, which is a filename optionally followed by flags:
//
//	/sys/class/net/eno1/statistics/rx_bytes:r
//
// The filename "-" is standard input. The only flag is "r" for rewind; the
// original also has "n" for a nonblocking open, which is not needed here
// because a poll that finds nothing simply reads nothing.
//
// The pipeline may be nil, in which case numbers pass through untouched.
func OpenSource(spec string, pipe *Pipeline) (*Source, error) {
	name, flags, _ := strings.Cut(spec, ":")

	s := &Source{Name: name, pipe: pipe, buf: make([]byte, 4096)}
	if s.pipe == nil {
		s.pipe = NewPipeline()
	}

	for _, f := range flags {
		switch f {
		case 'r':
			s.Rewind = true
		case 'n':
			// Accepted so that a command line written for the original still
			// runs. Polling already does not block on an empty file.
		default:
			return nil, fmt.Errorf("plot: %s: unknown input flag %q", name, string(f))
		}
	}

	if name == "-" {
		s.Name = "stdin"
		s.r = os.Stdin
		if s.Rewind {
			return nil, errors.New("plot: stdin cannot be rewound")
		}
		return s, nil
	}

	f, err := os.Open(name) //nolint:gosec // the path is the file the caller asked to plot
	if err != nil {
		return nil, err
	}
	s.r, s.closer, s.seeker = f, f, f

	if s.Rewind && s.seeker == nil {
		return nil, fmt.Errorf("plot: %s cannot be rewound", name)
	}
	return s, nil
}

// NewSource wraps an already-open reader.
func NewSource(name string, r io.Reader, pipe *Pipeline) *Source {
	s := &Source{Name: name, r: r, pipe: pipe, buf: make([]byte, 4096)}
	if s.pipe == nil {
		s.pipe = NewPipeline()
	}
	s.seeker, _ = r.(io.Seeker)
	return s
}

// Pipeline is the processing the numbers go through on their way out. It is
// never nil: a source with no pipeline has an empty one.
func (s *Source) Pipeline() *Pipeline { return s.pipe }

// Close releases the source if it owns anything.
func (s *Source) Close() error {
	if s.closer != nil {
		return s.closer.Close()
	}
	return nil
}

// Done reports whether the source has ended and will produce nothing more. A
// rewinding source is never done.
func (s *Source) Done() bool { return s.done }

// Read takes whatever is available now and returns the numbers it produced,
// after the pipeline. It does not wait for more.
//
// The returned slice is only valid until the next call.
func (s *Source) Read() ([]float64, error) {
	if s.done {
		return nil, nil
	}

	var raw []float64
	collect := func(v float64) { raw = append(raw, v) }

	n, err := s.r.Read(s.buf)
	if n > 0 {
		s.split.Write(s.buf[:n], collect)
	}

	switch {
	case err == nil:
		// More to come; a partial number at the end of the chunk stays held.

	case errors.Is(err, io.EOF):
		// Whatever was held is a whole number after all: nothing follows it.
		s.split.Flush(collect)

		switch {
		case s.Rewind && s.seeker != nil:
			if _, serr := s.seeker.Seek(0, io.SeekStart); serr != nil {
				return nil, fmt.Errorf("plot: %s: %w", s.Name, serr)
			}
		case s.Infinite:
			// Stay where we are and read again next time: what has not been
			// written yet is not the end of anything.

		default:
			s.done = true
		}

	default:
		return nil, fmt.Errorf("plot: %s: %w", s.Name, err)
	}

	if len(raw) == 0 && !s.done {
		return nil, nil
	}

	out := s.pipe.Process(raw)
	if s.done {
		// The stream has ended, so a stage waiting for a window to fill is
		// waiting for something that will not come. Copied first: Process
		// returns a slice the pipeline reuses.
		if tail := s.pipe.Flush(); len(tail) > 0 {
			out = append(append([]float64(nil), out...), tail...)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// ReadAll drains the source, which is what a source that is not being followed
// wants. Rewind and Infinite are both ignored here: either one would mean the
// source never ends, and this has to.
func (s *Source) ReadAll() ([]float64, error) {
	rewind, infinite := s.Rewind, s.Infinite
	s.Rewind, s.Infinite = false, false
	defer func() { s.Rewind, s.Infinite = rewind, infinite }()

	var out []float64
	for !s.done {
		got, err := s.Read()
		if err != nil {
			return out, err
		}
		out = append(out, got...)
	}
	return out, nil
}
