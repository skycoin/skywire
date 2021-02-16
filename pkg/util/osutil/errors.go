package osutil

// ErrorWithStderr is an error raised by the external process.
// `Err` is an actual error coming from `exec`, while `Stderr` contains
// stderr output of the process.
type ErrorWithStderr struct {
	Err    error
	Stderr []byte
}

// NewErrorWithStderr constructs new `ErrorWithStderr`.
func NewErrorWithStderr(err error, stderr []byte) *ErrorWithStderr {
	return &ErrorWithStderr{
		Err:    err,
		Stderr: stderr,
	}
}

// Error implements `error`.
func (e *ErrorWithStderr) Error() string {
	return e.Err.Error() + ": " + string(e.Stderr)
}
