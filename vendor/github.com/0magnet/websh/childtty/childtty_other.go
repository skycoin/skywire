//go:build !js

package childtty

import (
	"io"
	"os"
	"strconv"

	"github.com/gdamore/tcell/v3/tty"

	"github.com/0magnet/websh/progressive"
)

// Terminal, when set, is the terminal of a program its host runs in its own
// process, as a window's tab runs one: Open uses it in place of /dev/tty.
var Terminal func() *os.File

// Open returns this program's terminal, /dev/tty, or false when it has none.
// It puts the terminal in raw mode and asks the host what it offers before
// anything else reads it; progressive.Current has the answer. tcell starting
// it again finds it started, so nothing typed meanwhile is flushed, and every
// read after comes through the probe's leftovers and the event filter.
func Open() (tty.Tty, bool) {
	t, err := tty.NewDevTtyFromDev(device())
	if err != nil {
		return nil, false
	}
	if err := t.Start(); err != nil {
		return nil, false
	}
	c := &devTty{Tty: t}
	_, in, _ := progressive.Probe(t, t, probeWait) //nolint:errcheck // no answer means cells only; in is the input from here either way
	c.in, events = progressive.Filter(in)
	return c, true
}

// device is the path of the terminal Open opens. Terminal's file is opened
// again by its descriptor, read without File.Fd, which would make it blocking.
func device() string {
	if Terminal == nil {
		return "/dev/tty"
	}
	f := Terminal()
	if f == nil {
		return "/dev/tty"
	}
	rc, err := f.SyscallConn()
	if err != nil {
		return "/dev/tty"
	}
	dev := "/dev/tty"
	rc.Control(func(fd uintptr) { dev = "/dev/fd/" + strconv.Itoa(int(fd)) }) //nolint:errcheck,gosec // a closed file leaves /dev/tty
	return dev
}

// devTty is tcell's /dev/tty, read through the filter.
type devTty struct {
	tty.Tty
	in io.Reader
}

func (d *devTty) Read(p []byte) (int, error) { return d.in.Read(p) }
