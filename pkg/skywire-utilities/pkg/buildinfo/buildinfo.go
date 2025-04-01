// Package buildinfo pkg/skywire-utilities/pkg/buildinfo/buildinfo.go
package buildinfo

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime/debug"
)

const unknown = "unknown"

//$ go build -mod=vendor -ldflags="-X 'github.com/skycoin/skywire-utilities/pkg/buildinfo.version=$(git describe)' -X 'github.com/skycoin/skywire-utilities/pkg/buildinfo.date=$(date -u "+%Y-%m-%dT%H:%M:%SZ")' -X 'github.com/skycoin/skywire-utilities/pkg/buildinfo.commit=$(git rev-list -1 HEAD)'" .

var (
	version   = unknown
	commit    = unknown
	date      = unknown
	goversion = unknown
	bi        *debug.BuildInfo
)

// TODO: deprecate?
// $ go build -ldflags="-X 'github.com/skycoin/skywire-utilities/pkg/buildinfo.golist=$(go list -m -json -mod=mod github.com/skycoin/<repo>@<branch>)' -X 'github.com/skycoin/skywire-utilities/pkg/buildinfo.date=$(date -u "+%Y-%m-%dT%H:%M:%SZ")'" .
var golist string

// ModuleInfo represents the JSON structure returned by `go list -m -json`.
type ModuleInfo struct {
	Version string `json:"Version"`
	Origin  struct {
		Hash string `json:"Hash"`
	} `json:"Origin"`
}

func init() {
	if golist != "" {
		var mInfo ModuleInfo
		if err := json.Unmarshal([]byte(golist), &mInfo); err == nil {
			if mInfo.Version != "" && version == unknown {
				version = mInfo.Version
			}
			if mInfo.Origin.Hash != "" && commit == unknown {
				commit = mInfo.Origin.Hash
			}
		}
	}
	var ok bool
	if Get().Version == unknown || Get().Version == "" {
		bi, ok = debug.ReadBuildInfo()
		if ok {
			if bi.Main.Version != "" {
				version = bi.Main.Version
			}
			if bi.GoVersion != "" {
				goversion = bi.Main.Version
			}
		}
	}
}

// Version returns version from the parsed module info.
func Version() string {
	return version
}

// Go returns version of golang used for compilation from debug.ReadBuildInfo().
func Go() string {
	return goversion
}

// Commit returns commit hash from the parsed module info.
func Commit() string {
	return commit
}

// Date returns date of build in RFC3339 format.
func Date() string {
	return date
}

// DebugBuildInfo returns debug.BuildInfo.
func DebugBuildInfo() *debug.BuildInfo {
	return bi
}

// Get returns build info summary.
func Get() *Info {
	return &Info{
		Version: Version(),
		Commit:  Commit(),
		Date:    Date(),
		Go:      Go(),
	}
}

// Info is build info summary.
type Info struct {
	Go      string `json:"go"`
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
