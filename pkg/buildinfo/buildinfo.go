package buildinfo

import (
	"fmt"
	"io"
)

const unknown = "unknown"

var (
	Version = unknown
	Commit  = unknown
	Date    = unknown
)

func WriteTo(w io.Writer) (n int, err error) {
	msg := fmt.Sprintf("Version %q built on %q agaist commit %q\n", Version, Date, Commit)
	return w.Write([]byte(msg))
}
