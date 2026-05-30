package commands

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/calvin"
)

// withStdin swaps os.Stdin for the duration of a test and restores it.
func withStdin(t *testing.T, f *os.File) {
	t.Helper()
	orig := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = orig })
}

// charDeviceStdin points os.Stdin at /dev/null, which is a character device.
// This makes RunE take the non-piped path (args or no-input), the same as an
// interactive terminal would.
func charDeviceStdin(t *testing.T) {
	t.Helper()
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	withStdin(t, f)
}

// pipedStdin points os.Stdin at a regular temp file holding content. A regular
// file is not a character device, so RunE takes the stdin-reading path.
func pipedStdin(t *testing.T, content string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp stdin: %v", err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatalf("open temp stdin: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	withStdin(t, f)
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns what
// was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return string(out)
}

func TestRunE_Args(t *testing.T) {
	charDeviceStdin(t)

	var runErr error
	out := captureStdout(t, func() {
		runErr = RootCmd.RunE(RootCmd, []string{"hello"})
	})
	if runErr != nil {
		t.Fatalf("RunE returned error: %v", runErr)
	}
	want := calvin.AsciiFont("hello") + "\n"
	if out != want {
		t.Errorf("output mismatch\n got: %q\nwant: %q", out, want)
	}
}

func TestRunE_MultipleArgsJoined(t *testing.T) {
	charDeviceStdin(t)

	var runErr error
	out := captureStdout(t, func() {
		runErr = RootCmd.RunE(RootCmd, []string{"ab", "cd"})
	})
	if runErr != nil {
		t.Fatalf("RunE returned error: %v", runErr)
	}
	// Arguments are space-joined before rendering.
	want := calvin.AsciiFont("ab cd") + "\n"
	if out != want {
		t.Errorf("output mismatch\n got: %q\nwant: %q", out, want)
	}
}

func TestRunE_NoInput(t *testing.T) {
	charDeviceStdin(t)

	out := captureStdout(t, func() {
		err := RootCmd.RunE(RootCmd, nil)
		if err == nil {
			t.Error("expected error when no stdin and no args, got nil")
		} else if !strings.Contains(err.Error(), "no input provided") {
			t.Errorf("error = %v, want it to mention 'no input provided'", err)
		}
	})
	if out != "" {
		t.Errorf("expected no stdout on the no-input error, got %q", out)
	}
}

func TestRunE_Stdin(t *testing.T) {
	pipedStdin(t, "piped\n")

	var runErr error
	out := captureStdout(t, func() {
		// Even with args present, piped stdin takes precedence.
		runErr = RootCmd.RunE(RootCmd, []string{"ignored"})
	})
	if runErr != nil {
		t.Fatalf("RunE returned error: %v", runErr)
	}
	// The handler appends a newline per scanned line, so the rendered input is
	// "piped\n"; AsciiFont ignores the unknown '\n' rune.
	want := calvin.AsciiFont("piped\n") + "\n"
	if out != want {
		t.Errorf("output mismatch\n got: %q\nwant: %q", out, want)
	}
}

func TestRunE_StdinMultiLine(t *testing.T) {
	pipedStdin(t, "ab\ncd\n")

	var runErr error
	out := captureStdout(t, func() {
		runErr = RootCmd.RunE(RootCmd, nil)
	})
	if runErr != nil {
		t.Fatalf("RunE returned error: %v", runErr)
	}
	want := calvin.AsciiFont("ab\ncd\n") + "\n"
	if out != want {
		t.Errorf("output mismatch\n got: %q\nwant: %q", out, want)
	}
}

func TestRootCmdMetadata(t *testing.T) {
	if RootCmd.Use != "calvin" {
		t.Errorf("Use = %q, want calvin", RootCmd.Use)
	}
	if !strings.Contains(RootCmd.Long, "generate calvin ascii font") {
		t.Errorf("Long help missing expected text: %q", RootCmd.Long)
	}
}
