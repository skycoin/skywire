// Package dist pkg/hostbridge/dist/dist.go c3-vis-wasm
//
// Package dist holds the host bridge built with TinyGo for each platform, so
// a browser visor can hand its user the one for their host. Only the js
// build embeds them. Rebuild them with `make hostbridge-dist`.
package dist

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"sort"
	"strings"
)

const prefix = "skywire-hostbridge-"

// Platform is one embedded build.
type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	// File is the name to save it under.
	File string `json:"file"`
	Size int64  `json:"size"`
}

// ErrNotFound is returned for a file that is not embedded.
var ErrNotFound = errors.New("hostbridge/dist: no such build")

// Platforms lists the embedded builds.
func Platforms() []Platform {
	entries, err := files.ReadDir(".")
	if err != nil {
		return nil
	}
	var out []Platform
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".gz")
		osArch := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".exe")
		goos, arch, ok := strings.Cut(osArch, "-")
		if !ok || !strings.HasPrefix(name, prefix) {
			continue
		}
		b, err := Open(name)
		if err != nil {
			continue
		}
		out = append(out, Platform{OS: goos, Arch: arch, File: name, Size: int64(len(b))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}

// Open returns the executable saved as file.
func Open(file string) ([]byte, error) {
	if strings.ContainsAny(file, "/\\") {
		return nil, ErrNotFound
	}
	gz, err := files.ReadFile(file + ".gz")
	if err != nil {
		return nil, ErrNotFound
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zr)
}
