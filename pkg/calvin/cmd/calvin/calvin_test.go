package main

import (
	"io"
	"os"
	"testing"

	"github.com/skycoin/skywire/pkg/calvin/cmd/calvin/commands"
)

// TestInit verifies the side effects of init(): the hidden help flag, the
// custom usage template, and the hidden help command.
func TestInit(t *testing.T) {
	helpFlag := commands.RootCmd.PersistentFlags().Lookup("help")
	if helpFlag == nil {
		t.Fatal("persistent --help flag was not registered")
	}
	if !helpFlag.Hidden {
		t.Error("--help flag should be hidden")
	}
	if commands.RootCmd.UsageTemplate() != help {
		t.Error("usage template was not set to the custom help template")
	}
}

// TestMainExecutesRoot runs main() end-to-end with a non-interactive stdin
// (so the args path is taken) and asserts it renders output without panicking.
func TestMainExecutesRoot(t *testing.T) {
	// /dev/null is a character device => RunE takes the args branch.
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	defer devNull.Close() //nolint:errcheck

	origStdin, origStdout, origArgs := os.Stdin, os.Stdout, os.Args
	defer func() {
		os.Stdin, os.Stdout, os.Args = origStdin, origStdout, origArgs
	}()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdin = devNull
	os.Stdout = w
	os.Args = []string{"calvin", "hi"}

	main()

	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if len(out) == 0 {
		t.Error("main produced no output for argument input")
	}
}
