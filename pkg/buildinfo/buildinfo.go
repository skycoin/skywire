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

func Version() string {
	return version
}

func Commit() string {
	return commit
}

func Date() string {
	return date
}

func Get() *Info {
	return &Info{
		Version: Version(),
		Commit:  Commit(),
		Date:    Date(),
	}
}

type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

func (info *Info) WriteTo(w io.Writer) (n int, err error) {
	msg := fmt.Sprintf("Version %q built on %q agaist commit %q\n", info.Version, info.Date, info.Commit)
	return w.Write([]byte(msg))
}
