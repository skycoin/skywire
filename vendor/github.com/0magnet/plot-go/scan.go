package plot

import (
	"io"
	"strconv"
)

// Splitter turns a byte stream into numbers, one chunk at a time.
//
// The original's parser is deliberately lenient: it scans for something that
// could start a number, tries to read one from there, and skips the byte if
// that fails. That is what lets plot be pointed at a log line, a CSV or
// /proc/loadavg without anything in between — the numbers are picked out of
// whatever the text happens to be.
//
// A number split across two reads is the reason this is a type rather than a
// function. The tail of a chunk may be half of a number whose other half has
// not arrived, so it is held until the next Write says how it ends.
type Splitter struct {
	tok []byte
}

// Write feeds a chunk in, calling fn for every complete number in it.
//
// A number at the very end of the chunk is not complete — more digits may
// follow in the next one — so it is held back until Write is called again or
// Flush says the stream has ended.
func (s *Splitter) Write(p []byte, fn func(float64)) {
	for _, c := range p {
		switch {
		case c >= '0' && c <= '9':
			s.tok = append(s.tok, c)
		case c == '.' || c == 'e' || c == 'E' || c == '+':
			// Only meaningful inside a number; on their own they are noise.
			if len(s.tok) > 0 {
				s.tok = append(s.tok, c)
			}
		case c == '-':
			// Either the sign of a new number or an exponent's sign. Anywhere
			// else it ends the current one, so a range like "3-4" is two
			// numbers — as it is in the original, whose scanner accepts a
			// leading '-' and hands the rest to strtod.
			if len(s.tok) > 0 && (s.tok[len(s.tok)-1] == 'e' || s.tok[len(s.tok)-1] == 'E') {
				s.tok = append(s.tok, c)
			} else {
				s.emit(fn)
				s.tok = append(s.tok, c)
			}
		default:
			s.emit(fn)
		}
	}
}

// Flush emits a number left at the end of the stream. Call it at EOF, and only
// then: a file often ends without a trailing newline, and that last number is
// as real as any other.
func (s *Splitter) Flush(fn func(float64)) { s.emit(fn) }

func (s *Splitter) emit(fn func(float64)) {
	if len(s.tok) == 0 {
		return
	}
	if v, err := strconv.ParseFloat(string(s.tok), 64); err == nil {
		fn(v)
	}
	s.tok = s.tok[:0]
}

// Scan reads numbers out of r until it ends, ignoring everything else.
func Scan(r io.Reader, fn func(float64)) error {
	var (
		s   Splitter
		buf = make([]byte, 4096)
	)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			s.Write(buf[:n], fn)
		}
		if err != nil {
			s.Flush(fn)
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}
