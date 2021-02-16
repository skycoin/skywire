package osutil

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Run runs binary `bin` with `args`.
func Run(bin string, args ...string) error {
	return run(bin, os.Stdout, args...)
}

// RunWithResult runs binary `bin` with `args` returning stdout contents.
func RunWithResult(bin string, args ...string) (io.Reader, error) {
	stdout := bytes.NewBuffer(nil)

	return stdout, run(bin, stdout, args...)
}

func run(bin string, stdout io.Writer, args ...string) error {
	fullCmd := bin + " " + strings.Join(args, " ")

	cmd := exec.Command(bin, args...) //nolint:gosec

	stderrBuf := bytes.NewBuffer(nil)

	cmd.Stderr = io.MultiWriter(os.Stderr, stderrBuf)
	cmd.Stdout = stdout
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		return NewErrorWithStderr(fmt.Errorf("error running command \"%s\": %w", fullCmd, err),
			stderrBuf.Bytes())
	}

	return nil
}
