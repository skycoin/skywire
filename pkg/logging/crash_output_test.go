package logging

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

const crashOutputChildEnv = "SKYWIRE_CRASH_OUTPUT_CHILD"

// TestCrashOutputToKeepsGoroutineTrace runs a child that panics on a goroutine
// under GOTRACEBACK=none, the value the runtime forces on a binary with file
// capabilities. Plain SetCrashOutput records two lines; CrashOutputTo records
// the panicking goroutine's stack.
func TestCrashOutputToKeepsGoroutineTrace(t *testing.T) {
	for _, tc := range []struct {
		mode      string
		wantTrace bool
	}{
		{mode: "plain", wantTrace: false},
		{mode: "fixed", wantTrace: true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			crashPath := filepath.Join(t.TempDir(), "crash.log")
			cmd := exec.Command(os.Args[0], "-test.run=^TestCrashOutputChild$") //nolint:gosec
			cmd.Env = append(os.Environ(), "GOTRACEBACK=none", crashOutputChildEnv+"="+tc.mode+":"+crashPath)
			if err := cmd.Run(); err == nil {
				t.Fatal("child exited cleanly, want a crash")
			}
			out, err := os.ReadFile(crashPath) //nolint:gosec
			if err != nil {
				t.Fatal(err)
			}
			text := string(out)
			if !strings.Contains(text, "panic: crash output test") {
				t.Fatalf("crash file lacks the panic line:\n%s", text)
			}
			gotTrace := strings.Contains(text, "goroutine ") && strings.Contains(text, "crashOutputPanicOnGoroutine")
			if gotTrace != tc.wantTrace {
				t.Fatalf("goroutine trace present = %v, want %v:\n%s", gotTrace, tc.wantTrace, text)
			}
		})
	}
}

// TestCrashOutputChild is the crashing child of TestCrashOutputToKeepsGoroutineTrace.
func TestCrashOutputChild(t *testing.T) {
	spec := os.Getenv(crashOutputChildEnv)
	if spec == "" {
		t.Skip("run only as the child of TestCrashOutputToKeepsGoroutineTrace")
	}
	mode, crashPath, _ := strings.Cut(spec, ":")
	f, err := os.Create(crashPath) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if mode == "fixed" {
		err = CrashOutputTo(f)
	} else {
		err = debug.SetCrashOutput(f, debug.CrashOptions{})
	}
	if err != nil {
		t.Fatal(err)
	}
	block := make(chan struct{})
	go crashOutputPanicOnGoroutine()
	<-block
}

func crashOutputPanicOnGoroutine() {
	panic("crash output test")
}
