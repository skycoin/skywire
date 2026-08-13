// Package buildinfo pkg/buildinfo/buildinfo.go c0-com-util
//
// The encoding/json-using init() that parses ldflags-injected
// `go list -m -json` output lives in buildinfo_native.go (!js).
// Under js/wasm, ldflags injection is not the typical path —
// the install-page WASM is rebuilt against the latest skywire
// dependency, not with custom -X overrides — so the json parse
// is effectively dead code there. Build-tag-gating it lets
// TinyGo strip encoding/json's reflect runtime helpers from the
// WASM bundle.
package buildinfo

import (
	"fmt"
	"io"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
)

const unknown = "unknown"

// Variables set via -ldflags during build
// $ go build -mod=vendor -ldflags="-X 'github.com/skycoin/skywire/pkg/buildinfo.version=$(git describe)' -X 'github.com/skycoin/skywire/pkg/buildinfo.date=$(date -u "+%Y-%m-%dT%H:%M:%SZ")' -X 'github.com/skycoin/skywire/pkg/buildinfo.commit=$(git rev-list -1 HEAD)'" .
var (
	version   = unknown
	commit    = unknown
	date      = unknown
	goversion = ""
	buildtags = ""    // build tags recorded by the toolchain (debug.BuildInfo "-tags")
	modified  = false // vcs.modified — the build tree had uncommitted changes
)

// format hint: bi.Main.Version = v1.3.29-rc7.0.20250410212328-dc5d22b7ab2a
var bi *debug.BuildInfo

// ldflags-provided module info (`go list -m -json`)
// $ go build -ldflags="-X 'github.com/skycoin/skywire/pkg/buildinfo.golist=$(go list -m -json -mod=mod github.com/skycoin/<repo>@<branch>)' -X 'github.com/skycoin/skywire/pkg/buildinfo.date=$(date -u "+%Y-%m-%dT%H:%M:%SZ")'" .
var golist string

// ModuleInfo represents the JSON structure returned by `go list -m -json`.
type ModuleInfo struct {
	Version string `json:"Version"`
	Origin  struct {
		Hash string `json:"Hash"`
	} `json:"Origin"`
}

// Regular expressions for commit hash and timestamp
var commitRegex = regexp.MustCompile(`[a-f0-9]{12,}$`) // <-- match commit from end of string
var dateRegex = regexp.MustCompile(`\d{14}`)           // <-- match date anywhere

// init() is split across buildinfo_native.go (!js) and
// buildinfo_js.go (js). The !js variant parses ldflags-injected
// `golist` via json.Unmarshal; the js variant skips that branch
// (no encoding/json in the WASM build graph). Both call
// readDebugBuildInfo to populate `bi` + `goversion`.
//
// readDebugBuildInfo is shared between the two — no json, just
// debug.ReadBuildInfo plus the version/commit/date extraction.
func readDebugBuildInfo() {
	var ok bool
	bi, ok = debug.ReadBuildInfo()
	if !ok {
		return
	}
	if version == unknown || version == "" {
		if bi.Main.Version != "" {
			parseVersionInfo(bi.Main.Version)
		}
	}
	if bi.GoVersion != "" {
		goversion = bi.GoVersion
	}
	// Fall back to the VCS + build settings the Go toolchain records for
	// every build (including `go install <pkg>@<version>`, which carries
	// no ldflags) when the -X vars weren't injected. `vcs.revision` /
	// `vcs.time` are present only for builds from a VCS working tree;
	// `-tags` is present for any build. This lets `go install` binaries
	// self-describe their commit / date / build tags without the Makefile.
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if (commit == unknown || commit == "") && s.Value != "" {
				commit = s.Value
			}
		case "vcs.time":
			// Already RFC3339 (e.g. 2026-08-01T12:00:00Z).
			if (date == unknown || date == "") && s.Value != "" {
				date = s.Value
			}
		case "-tags":
			if s.Value != "" {
				buildtags = s.Value
			}
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
}

func parseVersionInfo(ver string) {
	// Extract commit
	if match := commitRegex.FindString(ver); match != "" {
		commit = match
		ver = strings.TrimSuffix(ver, "-"+commit)
	}

	// Extract date
	if match := dateRegex.FindString(ver); match != "" {
		date = formatBuildDate(match)
		ver = strings.Replace(ver, match, "", 1)
		ver = strings.TrimSuffix(ver, "-") // remove trailing dash
		ver = strings.TrimSuffix(ver, ".") // remove trailing dot
	}

	// What's left is version
	version = ver
}

// formatBuildDate converts a 14-digit timestamp into RFC3339 format
func formatBuildDate(dateStr string) string {
	if len(dateStr) != 14 {
		return unknown
	}
	return fmt.Sprintf("%s-%s-%sT%s:%s:%sZ",
		dateStr[0:4], dateStr[4:6], dateStr[6:8], // YYYY-MM-DD
		dateStr[8:10], dateStr[10:12], dateStr[12:14], // HH:MM:SS
	)
}

// Version returns the extracted version string.
func Version() string {
	if version == "(devel)" || version == "" {
		return unknown
	}
	return version
}

// DBIVersion returns bi.Main.Version.
func DBIVersion() string {
	if bi != nil {
		return bi.Main.Version
	}
	return ""
}

// Go returns the Go compiler version used for the build.
func Go() string {
	return goversion
}

// Commit returns the extracted commit hash.
func Commit() string {
	return commit
}

// VersionOrCommit returns Version() when a real (non-devel) version is known,
// otherwise a "dev-<short-commit>[-dirty]" string derived from the VCS info the
// Go/TinyGo toolchain records automatically (vcs.revision / vcs.modified). It
// lets a build that injects no version via ldflags — the wasm-visor, whose blob
// is compiled at an earlier commit than the binary that embeds it and so cannot
// borrow the host's version — still self-describe the commit it was built from
// instead of reporting "unknown". Returns unknown only when neither a version
// nor a commit is recorded.
func VersionOrCommit() string {
	if v := Version(); v != unknown {
		return v
	}
	if commit != unknown && commit != "" {
		short := commit
		if len(short) > 12 {
			short = short[:12]
		}
		if modified {
			return "dev-" + short + "-dirty"
		}
		return "dev-" + short
	}
	return unknown
}

// Date returns the extracted build date in RFC3339 format.
func Date() string {
	return date
}

// BuildTags returns the build tags recorded by the Go toolchain (the
// "-tags" build setting), or "" if none / unavailable.
func BuildTags() string {
	return buildtags
}

// DebugBuildInfo returns the raw debug.BuildInfo struct.
func DebugBuildInfo() *debug.BuildInfo {
	return bi
}

// Get returns a summary of build information.
func Get() *Info {
	// Build complete version string with commit
	ver := Version()
	if c := Commit(); c != "" && c != unknown && !strings.Contains(ver, c) {
		// Append commit to version if not already present
		if len(c) > 12 {
			c = c[:12]
		}
		ver = ver + "-" + c
	}
	// Note: Go's module system already adds +dirty to version when built from dirty repo
	return &Info{
		Version:   ver,
		Commit:    Commit(),
		Date:      Date(),
		Go:        Go(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		BuildTags: BuildTags(),
	}
}

// DepVersion returns the version of a specific dependency module.
// modulePath should be something like "github.com/skycoin/skycoin"
func DepVersion(modulePath string) string {
	if bi == nil {
		return ""
	}
	for _, dep := range bi.Deps {
		if dep.Path == modulePath {
			return dep.Version
		}
	}
	return ""
}

// Info represents build metadata.
type Info struct {
	Go        string `json:"go,omitempty"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	BuildTags string `json:"build_tags,omitempty"`
}

// WriteTo writes build info summary to an io.Writer.
func (info *Info) WriteTo(w io.Writer) (int64, error) {
	msg := fmt.Sprintf("Version %q built on %q against commit %q\n", info.Version, info.Date, info.Commit)
	n, err := w.Write([]byte(msg))
	return int64(n), err
}
