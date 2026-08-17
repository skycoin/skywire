// Package shell wires the sh interpreter to an in-memory filesystem
// and a set of built-in applets, forming a self-contained bash-like
// shell that runs anywhere Go runs — including js/wasm, where it backs
// an xterm-go terminal.
package shell

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/0magnet/afero"
	"github.com/0magnet/sh/v3/expand"
	"github.com/0magnet/sh/v3/interp"
	"github.com/0magnet/sh/v3/syntax"
)

// Shell is an interpreter bound to a virtual filesystem.
type Shell struct {
	FS     afero.Fs
	Runner *interp.Runner

	// RawMode, when set by the embedder, toggles raw terminal input:
	// no echo and no newline translation (used by full-screen applets
	// like less).
	RawMode func(on bool)
	// Size, when set, reports the terminal dimensions.
	Size func() (cols, rows int)

	parser  *syntax.Parser
	pending strings.Builder // continuation lines of an incomplete input
}

// New creates a shell over the given filesystem (nil = fresh in-memory
// fs seeded with a small home directory).
func New(vfs afero.Fs, stdin io.Reader, stdout, stderr io.Writer) (*Shell, error) {
	if vfs == nil {
		vfs = afero.NewMemMapFs()
		if err := Seed(vfs); err != nil {
			return nil, err
		}
	}
	s := &Shell{
		FS:     vfs,
		parser: syntax.NewParser(),
	}

	runner, err := interp.New(
		interp.StdIO(stdin, stdout, stderr),
		interp.Env(expand.ListEnviron(
			"HOME=/home/user",
			"USER=user",
			"HOSTNAME=websh",
			"PATH=/bin",
			"SHELL=/bin/websh",
			"TERM=xterm-256color",
			"PS1=$ ",
		)),
		interp.CallHandler(s.callHandler),
		interp.ExecHandlers(s.execHandler),
		interp.OpenHandler(s.openHandler),
		interp.StatHandler(s.statHandler),
		interp.ReadDirHandler2(s.readDirHandler),
		interp.AccessHandler(s.accessHandler),
	)
	if err != nil {
		return nil, err
	}
	// interp.Dir validates against the OS filesystem, so set the
	// virtual cwd directly instead
	runner.Dir = "/home/user"
	s.Runner = runner
	if err := s.PopulateBin(); err != nil {
		return nil, err
	}
	return s, nil
}

// UseHistory gives the interpreter's history builtin a history list. The
// interpreter never reads input lines itself, so only the line editor above it
// has one; call this with [LineEditor.History] and [LineEditor.ClearHistory]
// once the editor exists.
func (s *Shell) UseHistory(list func() []string, clear func()) error {
	return interp.History(list, clear)(s.Runner)
}

// PopulateBin creates a stub file in /bin for every registered applet
// so the command set is discoverable with ls. Call again after
// registering extra applets.
func (s *Shell) PopulateBin() error {
	if err := s.FS.MkdirAll("/bin", 0o755); err != nil {
		return err
	}
	for name, a := range applets {
		if err := afero.WriteFile(s.FS, "/bin/"+name,
			[]byte("websh built-in applet: "+a.help+"\n"), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// Seed populates a fresh filesystem with the default home directory.
func Seed(vfs afero.Fs) error {
	dirs := []string{"/home/user", "/tmp", "/etc", "/bin"}
	for _, d := range dirs {
		if err := vfs.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	files := map[string]string{
		"/etc/motd": "welcome to websh — a bash-like shell running entirely in your browser\n",
		"/home/user/readme.md": "# websh\n\n" +
			"This shell is github.com/0magnet/sh (a fork of mvdan.cc/sh) running\n" +
			"in WebAssembly behind an xterm-go terminal. The filesystem you are\n" +
			"looking at lives in memory.\n\n" +
			"Things to try:\n" +
			"  ls /bin        # every available command lives here\n" +
			"  help           # ...with descriptions\n" +
			"  ls -l /etc\n" +
			"  cat /etc/motd\n" +
			"  echo hello > hi.txt; cat hi.txt\n" +
			"  for i in $(seq 3); do echo line $i; done\n" +
			"  seq 10 | grep 1\n" +
			"  source demo.sh\n",
		"/home/user/demo.sh": "echo \"hello from a shell script!\"\n" +
			"for f in /etc/*; do echo \"found: $f\"; done\n" +
			"x=$((6 * 7))\n" +
			"echo \"the answer is $x\"\n",
	}
	for path, content := range files {
		if err := afero.WriteFile(vfs, path, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Dir returns the current working directory.
func (s *Shell) Dir() string { return s.Runner.Dir }

// Pending reports whether the shell is waiting for continuation lines
// of an incomplete statement (e.g. an unterminated for loop).
func (s *Shell) Pending() bool { return s.pending.Len() > 0 }

// CancelPending discards buffered continuation lines (Ctrl+C).
func (s *Shell) CancelPending() { s.pending.Reset() }

// Run feeds one input line to the shell. It returns needMore=true when
// the statement is incomplete and further lines are expected.
func (s *Shell) Run(ctx context.Context, line string) (needMore bool, err error) {
	s.pending.WriteString(line)
	s.pending.WriteString("\n")
	src := s.pending.String()

	file, err := s.parser.Parse(strings.NewReader(src), "websh")
	if err != nil {
		if syntax.IsIncomplete(err) {
			return true, nil
		}
		s.pending.Reset()
		return false, err
	}
	s.pending.Reset()
	err = s.Runner.Run(ctx, file)
	if s.Runner.Exited() {
		// plain `exit` in the top level shell: reset so the terminal
		// session keeps working
		s.Runner.Reset()
	}
	return false, err
}

// resolve makes a path absolute against the interpreter cwd.
func resolve(ctx context.Context, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(interp.HandlerCtx(ctx).Dir, path)
}

// discard is a /dev/null ReadWriteCloser.
type discard struct{}

func (discard) Read(p []byte) (int, error)  { return 0, io.EOF }
func (discard) Write(p []byte) (int, error) { return len(p), nil }
func (discard) Close() error                { return nil }

func (s *Shell) openHandler(ctx context.Context, path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
	if path == "/dev/null" {
		return discard{}, nil
	}
	return s.FS.OpenFile(resolve(ctx, path), flag, perm)
}

func (s *Shell) statHandler(ctx context.Context, name string, followSymlinks bool) (fs.FileInfo, error) {
	return s.FS.Stat(resolve(ctx, name))
}

func (s *Shell) accessHandler(ctx context.Context, path string, mode interp.AccessMode) error {
	// the virtual filesystem has no permission model: anything that
	// exists is accessible
	_, err := s.FS.Stat(resolve(ctx, path))
	return err
}

func (s *Shell) readDirHandler(ctx context.Context, path string) ([]fs.DirEntry, error) {
	infos, err := afero.ReadDir(s.FS, resolve(ctx, path))
	if err != nil {
		return nil, err
	}
	entries := make([]fs.DirEntry, len(infos))
	for i, info := range infos {
		entries[i] = fs.FileInfoToDirEntry(info)
	}
	return entries, nil
}

// callHandler rewrites command names that collide with interpreter
// builtins that interp recognizes but does not implement (bash's
// `help`), so our applets can take over.
func (s *Shell) callHandler(ctx context.Context, args []string) ([]string, error) {
	if len(args) > 0 && args[0] == "help" {
		args = append([]string{"websh-help"}, args[1:]...)
	}
	return args, nil
}

// execHandler dispatches commands to applets instead of the OS.
func (s *Shell) execHandler(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		name := filepath.Base(args[0])
		if applet, ok := applets[name]; ok {
			hc := interp.HandlerCtx(ctx)
			code := applet.run(ctx, s, &hc, args[1:])
			if code != 0 {
				// A shell exit status is a single byte. Clamp instead of
				// letting an out-of-range applet return wrap around — 256
				// would otherwise be reported to the caller as success.
				if code < 0 || code > 255 {
					code = 1
				}
				return interp.ExitStatus(code)
			}
			return nil
		}
		fprintf(interp.HandlerCtx(ctx).Stderr, "websh: %s: command not found\n", args[0])
		return interp.ExitStatus(127)
	}
}
