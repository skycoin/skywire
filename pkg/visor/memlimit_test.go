// Package visor pkg/visor/memlimit_test.go c3-vis-core
package visor

import (
	"errors"
	"testing"
)

func TestParseMemorySize(t *testing.T) {
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
	)
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{in: "40MiB", want: 40 * mib},
		{in: "40mib", want: 40 * mib},
		{in: " 256MiB ", want: 256 * mib},
		{in: "1GiB", want: gib},
		{in: "1.5GiB", want: gib + gib/2},
		{in: "512KiB", want: 512 * kib},
		{in: "100MB", want: 100 * 1000 * 1000},
		{in: "2GB", want: 2 * 1000 * 1000 * 1000},
		{in: "4096B", want: 4096},
		{in: "4096", want: 4096},
		{in: "", wantErr: true},
		{in: "MiB", wantErr: true},
		{in: "forty MiB", wantErr: true},
		{in: "auto", wantErr: true},
	}
	for _, c := range cases {
		got, err := parseMemorySize(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseMemorySize(%q) = %d, want an error", c.in, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("parseMemorySize(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
}

func TestResolveMemoryLimit(t *testing.T) {
	const mib = 1024 * 1024
	unknown := func() int64 { return 0 }
	thousandMiB := func() int64 { return 1000 * mib }
	cases := []struct {
		name      string
		limit     string
		avail     func() int64
		want      int64
		noMemInfo bool // want errNoMemInfo
		wantErr   bool // want any other error
	}{
		// The iOS profile's value, above the floor: kept as is.
		{name: "ios profile", limit: "40MiB", avail: unknown, want: 40 * mib},
		{name: "at the floor", limit: "32MiB", avail: unknown, want: 32 * mib},
		{name: "below the floor", limit: "16MiB", avail: unknown, want: minMemoryLimit},
		{name: "zero", limit: "0MiB", avail: unknown, want: minMemoryLimit},
		{name: "desktop value", limit: "512MiB", avail: unknown, want: 512 * mib},
		{name: "auto with meminfo", limit: "auto", avail: thousandMiB, want: 600 * mib},
		{name: "auto on a small machine", limit: "auto", avail: func() int64 { return 40 * mib }, want: minMemoryLimit},
		// macOS and iOS: no /proc/meminfo, so "auto" has nothing to go on.
		{name: "auto without meminfo", limit: "auto", avail: unknown, noMemInfo: true},
		{name: "garbage", limit: "lots", avail: thousandMiB, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveMemoryLimit(c.limit, c.avail)
			switch {
			case c.noMemInfo:
				if !errors.Is(err, errNoMemInfo) {
					t.Fatalf("resolveMemoryLimit(%q) = %d, %v; want errNoMemInfo", c.limit, got, err)
				}
			case c.wantErr:
				if err == nil || errors.Is(err, errNoMemInfo) {
					t.Fatalf("resolveMemoryLimit(%q) = %d, %v; want a parse error", c.limit, got, err)
				}
			case err != nil || got != c.want:
				t.Fatalf("resolveMemoryLimit(%q) = %d, %v; want %d", c.limit, got, err, c.want)
			}
		})
	}
}

func TestFormatBytesNamesTheIOSLimit(t *testing.T) {
	// G1 greps the core's log for "GOMEMLIMIT set to 40MiB".
	if got := formatBytes(40 * 1024 * 1024); got != "40MiB" {
		t.Fatalf("formatBytes(40 MiB) = %q, want 40MiB", got)
	}
}
