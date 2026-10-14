//go:build !unix

package console

import (
	"context"
	"errors"
	"io"
	"os"
)

// Options configure a console; see the unix build.
type Options struct {
	Host         string
	Greeting     string
	Env          []string
	Exec         func(ctx context.Context, args []string) (code int, handled bool)
	AfterCommand func()
	In, Out      *os.File
}

var errNoConsole = errors.New("console: websh on a terminal needs a unix system")

// Run has no terminal to run on here.
func Run(Options) (int, error) { return 1, errNoConsole }

// Script has no OS shell to run in here.
func Script(io.Reader, string, []string, Options) (int, error) { return 1, errNoConsole }
