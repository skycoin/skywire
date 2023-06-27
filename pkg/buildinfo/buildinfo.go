// Package buildinfo pkg/buildinfo/buildinfo.go
package buildinfo

import (
	"fmt"
	"io"
)

const unknown = "unknown"

var (
	version = unknown
	commit  = unknown
	date    = unknown
)

// Version returns version from git describe.
func Version() string {
	return version
}

// Commit returns commit hash.
func Commit() string {
	return commit
}

// Date returns date of build in RFC3339 format.
func Date() string {
	return date
}

// Get returns build info summary.
func Get() *Info {
	return &Info{
		Version: Version(),
		Commit:  Commit(),
		Date:    Date(),
	}
}

// Info is build info summary.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// WriteTo writes build info summary to io.Writer.
func (info *Info) WriteTo(w io.Writer) (int64, error) {
	msg := fmt.Sprintf("Version %q built on %q against commit %q\n", info.Version, info.Date, info.Commit)
	n, err := w.Write([]byte(msg))
	return int64(n), err
}
