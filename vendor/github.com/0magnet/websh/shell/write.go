package shell

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Applet output goes to a terminal or to the next stage of a pipeline. When
// such a write fails there is nowhere left for the applet to report it — a
// diagnostic would go to the same broken stream — so the shell does what every
// other shell does and carries on. These wrappers exist so that the decision
// to drop the error is made deliberately, once and in one documented place,
// instead of being re-litigated at each of the hundred-odd call sites.
//
// Errors that are actionable — opening, reading, closing or statting a file —
// are still checked at the point of the call.

// fprintf writes formatted output, discarding any write error.
func fprintf(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...) //nolint:errcheck // see the comment above
}

// fprintln writes its operands followed by a newline, discarding any write error.
func fprintln(w io.Writer, a ...any) {
	_, _ = fmt.Fprintln(w, a...) //nolint:errcheck // see the comment above
}

// fprint writes its operands, discarding any write error.
func fprint(w io.Writer, a ...any) {
	_, _ = fmt.Fprint(w, a...) //nolint:errcheck // see the comment above
}

// write writes raw bytes, discarding any write error.
func write(w io.Writer, b []byte) {
	_, _ = w.Write(b) //nolint:errcheck // see the comment above
}

// closeRead closes a handle that was only read from. Its data has already been
// consumed, so a close failure tells the caller nothing it could act on. A
// handle that was *written* to is closed explicitly and its error returned,
// because there a failure means the write never landed.
func closeRead(c io.Closer) {
	_ = c.Close() //nolint:errcheck // see the comment above
}

// numericKey is the sort key used by `sort -n`. Input that is not a number
// sorts as zero, matching how sort(1) treats non-numeric lines.
func numericKey(line string) int {
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		return 0
	}
	return n
}

// readByte reads one byte of a terminal escape sequence. A read error yields 0,
// which matches no escape-sequence branch, so the sequence is ignored exactly
// as an unrecognized one would be.
func readByte(r io.ByteReader) byte {
	b, err := r.ReadByte()
	if err != nil {
		return 0
	}
	return b
}

// skipByte consumes one byte of an escape sequence whose value does not matter
// (the trailing '~' of ESC [ n ~, for instance).
func skipByte(r io.ByteReader) { readByte(r) }
