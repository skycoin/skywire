package stdio

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

// ErrReleaseNoncaptured is returned on attempt to release a capturer that hasn't started capturing
var ErrReleaseNoncaptured = errors.New("releasing non-captured output")

// OutputCapturer allows to capture output to stdout/stderr and hold it temporarily
// while giving ability to write to stdout through a separate writer
type OutputCapturer interface {
	// CaptureStdout starts capturing all output that is written to stdout/stderr
	// return a separate writer for writing to stdout
	CaptureStdout() (io.Writer, error)
	// Release captured output to the screen, as well as stop capturing any further stout/stderr output
	Release() error
}

func NewCapturer() OutputCapturer {
	return &outputCapturer{}
}

type outputCapturer struct {
	capturing    bool
	origStdoutFD int
	origStderrFD int
	stdoutReader *os.File
	stderrReader *os.File
	stdoutWriter *os.File
	stderrWriter *os.File
}

func (oc *outputCapturer) CaptureStdout() (io.Writer, error) {
	oc.capturing = true
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	oldStdout, err := syscall.Dup(syscall.Stdout)
	if err != nil {
		return nil, err
	}

	if err := DupTo(int(stdoutWriter.Fd()), syscall.Stdout); err != nil {
		return nil, err
	}

	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	oldStderr, err := syscall.Dup(syscall.Stderr)
	if err != nil {
		return nil, err
	}

	if err := DupTo(int(stderrWriter.Fd()), syscall.Stderr); err != nil {
		return nil, err
	}

	oc.origStdoutFD = oldStdout
	oc.origStderrFD = oldStderr
	oc.stdoutReader = stdoutReader
	oc.stderrReader = stderrReader
	oc.stdoutWriter = stdoutWriter
	oc.stderrWriter = stderrWriter

	origStdoutWriter := os.NewFile(uintptr(oc.origStdoutFD), "/dev/stdout")
	return origStdoutWriter, nil
}

func (oc *outputCapturer) Release() error {
	if !oc.capturing {
		return ErrReleaseNoncaptured
	}
	if err := oc.stdoutWriter.Close(); err != nil {
		return err
	}

	if err := oc.stderrWriter.Close(); err != nil {
		return err
	}

	if err := syscall.Close(syscall.Stdout); err != nil {
		return err
	}

	if err := syscall.Close(syscall.Stderr); err != nil {
		return err
	}

	if err := DupTo(oc.origStdoutFD, syscall.Stdout); err != nil {
		return err
	}

	if err := DupTo(oc.origStderrFD, syscall.Stderr); err != nil {
		return err
	}

	var stdoutBuffer bytes.Buffer
	var stderrBuffer bytes.Buffer

	if _, err := io.Copy(&stdoutBuffer, oc.stdoutReader); err != nil {
		return err
	}

	if _, err := io.Copy(&stderrBuffer, oc.stderrReader); err != nil {
		return err
	}

	if _, err := fmt.Fprint(os.Stdout, stdoutBuffer.String()); err != nil {
		return err
	}

	if _, err := fmt.Fprint(os.Stderr, stderrBuffer.String()); err != nil {
		return err
	}

	return nil
}
