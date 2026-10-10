package visor

import (
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
