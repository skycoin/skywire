package osutil

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
)

// Run runs binary `bin` with `args`.
func Run(bin string, args ...string) error {
	return run(bin, os.Stdout, args...)
}

// RunWithResultReader runs binary `bin` with `args` returning stdout contents as `io.Reader`.
func RunWithResultReader(bin string, args ...string) (io.Reader, error) {
	stdout := bytes.NewBuffer(nil)

	return stdout, run(bin, stdout, args...)
}

// RunWithResult runs binary `bin` with `args` returning stdout contents as bytes.
func RunWithResult(bin string, args ...string) ([]byte, error) {
	stdout, err := RunWithResultReader(bin, args...)
	if err != nil {
		return nil, err
	}

	stdoutBytes, err := ioutil.ReadAll(stdout)
	if err != nil {
		return nil, fmt.Errorf("failed to read stdout: %w", err)
	}

	return stdoutBytes, nil
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
