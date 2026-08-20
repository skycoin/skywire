package plot

import (
	"fmt"
	"strconv"
	"strings"
)

// BufferSize bounds a processor's window, matching PLOT_DBUF_SIZE in the
// original. It is not a limit on how much data can be plotted — samples stream
// through — only on how far back a single processor may look.
const BufferSize = 128

// Pipeline is a chain of processors. The output of each is the input of the
// next, so "avg:5|roc:5" first averages every five samples into one and then
// reports the rate of change of those averages.
//
// A Pipeline holds the buffered tail of every stage, so like the processors in
// it, one belongs to a single stream.
type Pipeline struct {
	procs []Proc
	// held[i] is what stage i has been given but has not consumed: a window
	// that is still filling. It is offered again, with more appended, on the
	// next Process.
	held [][]float64
	out  []float64
}

// NewPipeline chains the given processors.
func NewPipeline(procs ...Proc) *Pipeline {
	return &Pipeline{procs: procs, held: make([][]float64, len(procs))}
}

// Len reports how many stages the pipeline has.
func (p *Pipeline) Len() int { return len(p.procs) }

// Process runs samples through every stage and returns what came out the far
// end. The slice is only valid until the next call.
//
// An empty pipeline passes its input through, which is what makes a pipeline
// the single path for data whether or not any processing was asked for.
func (p *Pipeline) Process(in []float64) []float64 {
	cur := in
	for i, proc := range p.procs {
		// Whatever this stage kept last time comes first: it is the front of
		// the window it is still filling.
		p.held[i] = append(p.held[i], cur...)

		p.out = p.out[:0]
		out, consumed := proc.Process(p.out, p.held[i])
		p.out = out

		// Drop what was consumed, keep the rest. Copying to the front rather
		// than reslicing keeps the backing array from growing without bound on
		// a stream that never ends.
		if consumed > 0 {
			p.held[i] = append(p.held[i][:0], p.held[i][consumed:]...)
		}

		// Hand this stage's output to the next one. It has to be copied,
		// because p.out is reused by the next iteration.
		cur = append([]float64(nil), out...)
	}
	return cur
}

// Flush drains every stage, for when the stream has ended. A stage holding a
// window that will never fill emits what it has rather than losing it, and each
// stage's last output still passes through the ones after it.
//
// Call it once, at the end. A pipeline that has been flushed can still be given
// more samples, but the stages that held a partial window have started over.
func (p *Pipeline) Flush() []float64 {
	var cur []float64
	for i, proc := range p.procs {
		p.held[i] = append(p.held[i], cur...)

		var out []float64
		if f, ok := proc.(Flusher); ok {
			out = f.Flush(nil, p.held[i])
		} else {
			// Nothing special to flush, but it may still be holding samples,
			// and nothing more is coming to unblock them.
			out, _ = proc.Process(nil, p.held[i])
		}

		p.held[i] = p.held[i][:0]
		cur = out
	}
	return cur
}

// ParsePipeline reads the pipeline syntax used on the command line:
// processors separated by "|", each optionally taking an argument after ":".
//
//	avg:5|roc:5
//
// The names are those of the original, so a command line written for C plot
// means the same thing here.
func ParsePipeline(spec string) (*Pipeline, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return NewPipeline(), nil
	}

	var procs []Proc
	for _, elem := range strings.Split(spec, "|") {
		elem = strings.TrimSpace(elem)
		if elem == "" {
			return nil, fmt.Errorf("plot: empty pipeline element in %q", spec)
		}
		name, arg, hasArg := strings.Cut(elem, ":")

		intArg := func() (int, error) {
			if !hasArg {
				return 0, fmt.Errorf("plot: %s needs an argument, as in %s:5", name, name)
			}
			n, err := strconv.Atoi(arg)
			if err != nil {
				return 0, fmt.Errorf("plot: %s: %q is not a whole number", name, arg)
			}
			return n, nil
		}

		var (
			proc Proc
			err  error
		)
		switch name {
		case "avg":
			var n int
			if n, err = intArg(); err == nil {
				proc, err = NewBlock(n)
			}
		case "sma":
			var n int
			if n, err = intArg(); err == nil {
				proc, err = NewSimple(n)
			}
		case "cma":
			if hasArg {
				err = fmt.Errorf("plot: cma takes no argument")
			} else {
				proc = NewCumulative()
			}
		case "roc":
			// The interval is a duration between samples rather than a count,
			// so unlike the others it may be fractional.
			if !hasArg {
				proc, err = NewRate(1)
			} else {
				var f float64
				if f, err = strconv.ParseFloat(arg, 64); err != nil {
					err = fmt.Errorf("plot: roc: %q is not a number", arg)
				} else {
					proc, err = NewRate(f)
				}
			}
		default:
			return nil, fmt.Errorf("plot: unknown processor %q; known are avg, sma, cma and roc", name)
		}
		if err != nil {
			return nil, err
		}
		procs = append(procs, proc)
	}
	return NewPipeline(procs...), nil
}
