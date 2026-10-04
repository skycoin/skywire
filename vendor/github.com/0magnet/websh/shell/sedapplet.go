package shell

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/0magnet/afero"
	"github.com/0magnet/sh/v3/interp"

	"github.com/0magnet/websh/shell/sed"
)

const sedUsage = `usage: sed [-nEs] [-i[SUFFIX]] [-e script]... [-f file]... [script] [file...]
  -n, --quiet       no automatic printing
  -e script         add a script (several are joined by newlines)
  -f file           add the script in file
  -E, -r            extended regular expressions
  -i[SUFFIX]        edit files in place (keeping a backup if SUFFIX is given)
  -s                treat files as separate rather than one stream
`

type sedArgs struct {
	scripts  []string
	files    []string
	quiet    bool
	ere      bool
	separate bool
	inPlace  bool
	suffix   string
}

// parseSedArgs reads GNU sed's options, which may come after the operands and
// may be bundled (-ne, -nE, -i.bak).
func parseSedArgs(s *Shell, hc *interp.HandlerContext, args []string) (sedArgs, error) {
	var a sedArgs
	var operands []string
	haveScript := false
	addFile := func(name string) error {
		b, err := afero.ReadFile(s.FS, resolveArg(hc, name))
		if err != nil {
			return err
		}
		a.scripts = append(a.scripts, strings.TrimSuffix(string(b), "\n"))
		haveScript = true
		return nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			operands = append(operands, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(arg, "--"):
			name, val, hasVal := strings.Cut(arg[2:], "=")
			next := func() (string, error) {
				if hasVal {
					return val, nil
				}
				if i+1 >= len(args) {
					return "", errors.New("option '--" + name + "' requires an argument")
				}
				i++
				return args[i], nil
			}
			switch name {
			case "quiet", "silent":
				a.quiet = true
			case "regexp-extended":
				a.ere = true
			case "separate":
				a.separate = true
			case "in-place":
				a.inPlace, a.suffix = true, val
			case "expression":
				v, err := next()
				if err != nil {
					return a, err
				}
				a.scripts, haveScript = append(a.scripts, v), true
			case "file":
				v, err := next()
				if err != nil {
					return a, err
				}
				if err := addFile(v); err != nil {
					return a, err
				}
			case "posix", "unbuffered", "debug", "sandbox", "follow-symlinks":
			case "help":
				return a, errSedHelp
			default:
				return a, errors.New("unknown option -- '" + name + "'")
			}
		case len(arg) > 1 && arg[0] == '-':
		flags:
			for j := 1; j < len(arg); j++ {
				switch arg[j] {
				case 'n':
					a.quiet = true
				case 'E', 'r':
					a.ere = true
				case 's':
					a.separate = true
				case 'u', 'z':
					if arg[j] == 'z' {
						return a, errors.New("-z is not supported")
					}
				case 'i':
					a.inPlace, a.suffix = true, arg[j+1:]
					break flags
				case 'e', 'f':
					v := arg[j+1:]
					if v == "" {
						if i+1 >= len(args) {
							return a, errors.New("option requires an argument -- '" + string(arg[j]) + "'")
						}
						i++
						v = args[i]
					}
					if arg[j] == 'e' {
						a.scripts, haveScript = append(a.scripts, v), true
					} else if err := addFile(v); err != nil {
						return a, err
					}
					break flags
				case 'h':
					return a, errSedHelp
				default:
					return a, errors.New("invalid option -- '" + string(arg[j]) + "'")
				}
			}
		default:
			operands = append(operands, arg)
		}
	}
	if !haveScript {
		if len(operands) == 0 {
			return a, errSedHelp
		}
		a.scripts, operands = []string{operands[0]}, operands[1:]
	}
	a.files = operands
	if a.inPlace {
		a.separate = true
		if len(a.files) == 0 {
			return a, errors.New("no input files")
		}
	}
	return a, nil
}

var errSedHelp = errors.New("help")

func runSed(_ context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	a, err := parseSedArgs(s, hc, args)
	if errors.Is(err, errSedHelp) {
		fprint(hc.Stderr, sedUsage)
		return 1
	}
	if err != nil {
		fprintf(hc.Stderr, "sed: %v\n", err)
		return 1
	}

	var opened []io.Closer
	defer func() {
		for _, c := range opened {
			c.Close() //nolint:errcheck,gosec // nothing useful to do on the way out
		}
	}()
	prog, err := sed.Compile(strings.Join(a.scripts, "\n"), sed.Options{
		Quiet:    a.quiet,
		Extended: a.ere,
		ReadFile: func(name string) ([]byte, error) {
			if name == "/dev/stdin" {
				return io.ReadAll(hc.Stdin)
			}
			return afero.ReadFile(s.FS, resolveArg(hc, name))
		},
		OpenWrite: func(name string) (io.Writer, error) {
			switch name {
			case "/dev/stdout":
				return hc.Stdout, nil
			case "/dev/stderr":
				return hc.Stderr, nil
			}
			f, err := s.FS.OpenFile(resolveArg(hc, name), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if err != nil {
				return nil, err
			}
			opened = append(opened, f)
			return f, nil
		},
	})
	if err != nil {
		fprintf(hc.Stderr, "sed: %v\n", err)
		return 1
	}

	status := 0
	open := func(name string) (io.Reader, bool) {
		if name == "-" {
			return hc.Stdin, true
		}
		b, err := afero.ReadFile(s.FS, resolveArg(hc, name))
		if err != nil {
			fprintf(hc.Stderr, "sed: can't read %s: %v\n", name, unwrapPathErr(err))
			status = 2
			return nil, false
		}
		return bytes.NewReader(b), true
	}
	finish := func(err error) (stop bool, code int) {
		switch {
		case errors.Is(err, sed.ErrQuit):
			return true, prog.ExitCode()
		case err != nil:
			fprintf(hc.Stderr, "sed: %v\n", err)
			return true, 4
		}
		return false, 0
	}

	switch {
	case len(a.files) == 0:
		if stop, code := finish(prog.Run(hc.Stdout, hc.Stdin)); stop {
			return code
		}
	case a.inPlace:
		for _, name := range a.files {
			path := resolveArg(hc, name)
			info, err := s.FS.Stat(path)
			if err == nil && info.IsDir() {
				fprintf(hc.Stderr, "sed: couldn't edit %s: not a regular file\n", name)
				status = 4
				continue
			}
			r, ok := open(name)
			if !ok {
				continue
			}
			var buf bytes.Buffer
			stop, code := finish(prog.Run(&buf, r))
			if code == 4 {
				return 4
			}
			if a.suffix != "" {
				if err := sedBackup(s.FS, path, a.suffix); err != nil {
					fprintf(hc.Stderr, "sed: %v\n", err)
					return 4
				}
			}
			if err := afero.WriteFile(s.FS, path, buf.Bytes(), info.Mode().Perm()); err != nil {
				fprintf(hc.Stderr, "sed: %v\n", err)
				return 4
			}
			if stop {
				return code
			}
		}
	case a.separate:
		for _, name := range a.files {
			r, ok := open(name)
			if !ok {
				continue
			}
			if stop, code := finish(prog.Run(hc.Stdout, r)); stop {
				return code
			}
		}
	default:
		var rs []io.Reader
		for _, name := range a.files {
			if r, ok := open(name); ok {
				rs = append(rs, r)
			}
		}
		if stop, code := finish(prog.Run(hc.Stdout, rs...)); stop {
			return code
		}
	}
	return status
}

// sedBackup copies path aside before an in-place edit. A * in the suffix is
// replaced by the file's base name, as GNU sed does (-i 'bak/*').
func sedBackup(fs afero.Fs, path, suffix string) error {
	dest := path + suffix
	if strings.Contains(suffix, "*") {
		dest = strings.ReplaceAll(suffix, "*", filepath.Base(path))
		if !strings.Contains(dest, "/") {
			dest = filepath.Join(filepath.Dir(path), dest)
		} else if !filepath.IsAbs(dest) {
			dest = filepath.Join(filepath.Dir(path), dest)
		}
	}
	b, err := afero.ReadFile(fs, path)
	if err != nil {
		return err
	}
	return afero.WriteFile(fs, dest, b, 0o644)
}

func unwrapPathErr(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
