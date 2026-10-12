// Package visor pkg/visor/memlimit_test.go c3-vis-core
package visor

import (
	"errors"
	"math"
	"runtime/debug"
	"testing"

	"github.com/skycoin/skywire/pkg/logging"
)

func TestApplyMemoryLimitEnvWins(t *testing.T) {
	prev := debug.SetMemoryLimit(math.MaxInt64)
	t.Cleanup(func() { debug.SetMemoryLimit(prev) })
	log := logging.MustGetLogger("memlimit_test")

	t.Setenv("GOMEMLIMIT", "6400MiB")
	applyMemoryLimit(log, "256MiB")
	if got := debug.SetMemoryLimit(-1); got != math.MaxInt64 {
		t.Fatalf("config overrode the environment: limit %d", got)
	}

	t.Setenv("GOMEMLIMIT", "")
	applyMemoryLimit(log, "256MiB")
	if got := debug.SetMemoryLimit(-1); got != 256<<20 {
		t.Fatalf("config limit not applied: %d", got)
	}
}

func TestAutoMemoryLimit(t *testing.T) {
	const mib = 1024 * 1024
	if got := autoMemoryLimit(1000 * mib); got != 900*mib {
		t.Fatalf("autoMemoryLimit(1000MiB) = %d, want %d", got, 900*mib)
	}
	if got := autoMemoryLimit(0); got != 0 {
		t.Fatalf("autoMemoryLimit(0) = %d, want 0", got)
	}
}

func TestApplyMemoryLimitNone(t *testing.T) {
	before := debug.SetMemoryLimit(-1)
	for _, v := range []string{"none", ""} {
		applyMemoryLimit(logging.MustGetLogger("test"), v)
		if got := debug.SetMemoryLimit(-1); got != before {
			t.Fatalf("memory_limit %q changed the limit to %d", v, got)
		}
	}
}

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
		total     func() int64
		want      int64
		noMemInfo bool // want errNoMemInfo
		wantErr   bool // want any other error
	}{
		// The iOS profile's value, above the floor: kept as is.
		{name: "ios profile", limit: "40MiB", total: unknown, want: 40 * mib},
		{name: "at the floor", limit: "32MiB", total: unknown, want: 32 * mib},
		{name: "below the floor", limit: "16MiB", total: unknown, want: minMemoryLimit},
		{name: "zero", limit: "0MiB", total: unknown, want: minMemoryLimit},
		{name: "desktop value", limit: "512MiB", total: unknown, want: 512 * mib},
		{name: "auto with meminfo", limit: "auto", total: thousandMiB, want: 900 * mib},
		{name: "auto on a small machine", limit: "auto", total: func() int64 { return 30 * mib }, want: minMemoryLimit},
		// macOS and iOS: no /proc/meminfo or cgroup, so "auto" has nothing to go on.
		{name: "auto without meminfo", limit: "auto", total: unknown, noMemInfo: true},
		{name: "garbage", limit: "lots", total: thousandMiB, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveMemoryLimit(c.limit, c.total)
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
