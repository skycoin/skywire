package shell

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/0magnet/afero"
	"github.com/0magnet/sh/v3/expand"
	"github.com/0magnet/sh/v3/interp"
	"github.com/0magnet/u-root/pkg/ls"
)

// applet is a built-in utility. run returns the exit code.
type applet struct {
	help string
	run  func(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int
}

var applets map[string]applet

// registered in init to avoid an initialization cycle through help
func init() {
	applets = map[string]applet{
		"ls":       {"list directory contents (-l long, -a all)", runLs},
		"cat":      {"concatenate files to stdout", runCat},
		"mkdir":    {"create directories (-p parents)", runMkdir},
		"rmdir":    {"remove empty directories", runRmdir},
		"rm":       {"remove files (-r recursive, -f force)", runRm},
		"cp":       {"copy files (-r recursive)", runCp},
		"mv":       {"move/rename files", runMv},
		"touch":    {"create files / update timestamps", runTouch},
		"head":     {"first lines of files (-n count)", runHead},
		"tail":     {"last lines of files (-n count)", runTail},
		"wc":       {"count lines, words, bytes (-l -w -c)", runWc},
		"grep":     {"search with regexps (-i -v -n -c)", runGrep},
		"seq":      {"print number sequences", runSeq},
		"sort":     {"sort lines (-r reverse, -n numeric)", runSort},
		"uniq":     {"filter repeated lines (-c count)", runUniq},
		"tree":     {"directory tree", runTree},
		"basename": {"strip directory from path", runBasename},
		"dirname":  {"strip filename from path", runDirname},
		"date":     {"print the current time", runDate},
		"sleep":    {"delay for N seconds", runSleep},
		"clear":    {"clear the terminal", runClear},
		"env":      {"print the environment", runEnv},
		"which":    {"locate a command", runWhich},
		"uname":    {"system information", runUname},
		"hostname": {"print the hostname", runHostname},
		"help":     {"list available commands", runHelp},
	}
}

// AppletNames returns the registered applet names, sorted.
func AppletNames() []string {
	names := make([]string, 0, len(applets))
	for name := range applets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// RegisterApplet adds (or replaces) an applet — used by embedders to
// expose environment-specific commands.
func RegisterApplet(name, help string, run func(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int) {
	applets[name] = applet{help, run}
}

func fail(hc *interp.HandlerContext, name string, err error) int {
	fprintf(hc.Stderr, "%s: %v\n", name, err)
	return 1
}

// parseFlags splits leading -x flags from args (combined -abc allowed).
func parseFlags(args []string) (flags map[byte]bool, rest []string) {
	flags = map[byte]bool{}
	for i, a := range args {
		if len(a) > 1 && a[0] == '-' && a != "--" {
			// Flags are single ASCII letters. Iterate bytes rather than
			// runes so that a multi-byte rune cannot be truncated into a
			// byte that reads as some unrelated flag.
			for j := 1; j < len(a); j++ {
				flags[a[j]] = true
			}
			continue
		}
		if a == "--" {
			return flags, args[i+1:]
		}
		return flags, args[i:]
	}
	return flags, nil
}

func resolveArg(hc *interp.HandlerContext, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(hc.Dir, path)
}

func runLs(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	flags, rest := parseFlags(args)
	if len(rest) == 0 {
		rest = []string{"."}
	}
	var stringer ls.Stringer = ls.NameStringer{}
	if flags['l'] {
		stringer = ls.LongStringer{Human: flags['h'], Name: ls.NameStringer{}}
	}
	code := 0
	for _, arg := range rest {
		path := resolveArg(hc, arg)
		info, err := s.FS.Stat(path)
		if err != nil {
			code = fail(hc, "ls", err)
			continue
		}
		if !info.IsDir() {
			fprintln(hc.Stdout, stringer.FileString(ls.FromOSFileInfo(path, info)))
			continue
		}
		infos, err := afero.ReadDir(s.FS, path)
		if err != nil {
			code = fail(hc, "ls", err)
			continue
		}
		if len(rest) > 1 {
			fprintf(hc.Stdout, "%s:\n", arg)
		}
		for _, fi := range infos {
			if !flags['a'] && strings.HasPrefix(fi.Name(), ".") {
				continue
			}
			name := stringer.FileString(ls.FromOSFileInfo(filepath.Join(path, fi.Name()), fi))
			if fi.IsDir() && !flags['l'] {
				name += "/"
			}
			fprintln(hc.Stdout, name)
		}
	}
	return code
}

func runCat(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	_, rest := parseFlags(args)
	if len(rest) == 0 {
		if _, err := io.Copy(hc.Stdout, hc.Stdin); err != nil {
			return fail(hc, "cat", err)
		}
		return 0
	}
	for _, arg := range rest {
		f, err := s.FS.Open(resolveArg(hc, arg))
		if err != nil {
			return fail(hc, "cat", err)
		}
		_, err = io.Copy(hc.Stdout, f)
		closeRead(f)
		if err != nil {
			return fail(hc, "cat", err)
		}
	}
	return 0
}

func runMkdir(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	flags, rest := parseFlags(args)
	if len(rest) == 0 {
		fprintln(hc.Stderr, "mkdir: missing operand")
		return 1
	}
	for _, arg := range rest {
		path := resolveArg(hc, arg)
		var err error
		if flags['p'] {
			err = s.FS.MkdirAll(path, 0o755)
		} else {
			err = s.FS.Mkdir(path, 0o755)
		}
		if err != nil {
			return fail(hc, "mkdir", err)
		}
	}
	return 0
}

func runRmdir(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	_, rest := parseFlags(args)
	for _, arg := range rest {
		path := resolveArg(hc, arg)
		infos, err := afero.ReadDir(s.FS, path)
		if err != nil {
			return fail(hc, "rmdir", err)
		}
		if len(infos) > 0 {
			fprintf(hc.Stderr, "rmdir: %s: directory not empty\n", arg)
			return 1
		}
		if err := s.FS.Remove(path); err != nil {
			return fail(hc, "rmdir", err)
		}
	}
	return 0
}

func runRm(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	flags, rest := parseFlags(args)
	if len(rest) == 0 {
		fprintln(hc.Stderr, "rm: missing operand")
		return 1
	}
	for _, arg := range rest {
		path := resolveArg(hc, arg)
		info, err := s.FS.Stat(path)
		if err != nil {
			if flags['f'] {
				continue
			}
			return fail(hc, "rm", err)
		}
		if info.IsDir() && !flags['r'] {
			fprintf(hc.Stderr, "rm: %s: is a directory\n", arg)
			return 1
		}
		if err := s.FS.RemoveAll(path); err != nil && !flags['f'] {
			return fail(hc, "rm", err)
		}
	}
	return 0
}

func copyFile(s *Shell, src, dst string) error {
	in, err := s.FS.Open(src)
	if err != nil {
		return err
	}
	defer closeRead(in)
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := s.FS.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		closeRead(out) // the copy error is the one worth reporting
		return err
	}
	// Closing a file that was written to can still fail, and that failure
	// means the copy did not land — so it is returned rather than dropped.
	return out.Close()
}

func copyAny(s *Shell, src, dst string, recursive bool) error {
	info, err := s.FS.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		// copying into a directory keeps the base name
		if di, err := s.FS.Stat(dst); err == nil && di.IsDir() {
			dst = filepath.Join(dst, filepath.Base(src))
		}
		return copyFile(s, src, dst)
	}
	if !recursive {
		return fmt.Errorf("%s is a directory (use -r)", src)
	}
	if err := s.FS.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	infos, err := afero.ReadDir(s.FS, src)
	if err != nil {
		return err
	}
	for _, fi := range infos {
		if err := copyAny(s, filepath.Join(src, fi.Name()), filepath.Join(dst, fi.Name()), true); err != nil {
			return err
		}
	}
	return nil
}

func runCp(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	flags, rest := parseFlags(args)
	if len(rest) < 2 {
		fprintln(hc.Stderr, "cp: missing operand")
		return 1
	}
	dst := resolveArg(hc, rest[len(rest)-1])
	for _, arg := range rest[:len(rest)-1] {
		if err := copyAny(s, resolveArg(hc, arg), dst, flags['r']); err != nil {
			return fail(hc, "cp", err)
		}
	}
	return 0
}

func runMv(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	_, rest := parseFlags(args)
	if len(rest) < 2 {
		fprintln(hc.Stderr, "mv: missing operand")
		return 1
	}
	dst := resolveArg(hc, rest[len(rest)-1])
	for _, arg := range rest[:len(rest)-1] {
		src := resolveArg(hc, arg)
		target := dst
		if di, err := s.FS.Stat(dst); err == nil && di.IsDir() {
			target = filepath.Join(dst, filepath.Base(src))
		}
		if err := s.FS.Rename(src, target); err != nil {
			return fail(hc, "mv", err)
		}
	}
	return 0
}

func runTouch(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	_, rest := parseFlags(args)
	now := time.Now()
	for _, arg := range rest {
		path := resolveArg(hc, arg)
		if _, err := s.FS.Stat(path); err != nil {
			f, err := s.FS.Create(path)
			if err != nil {
				return fail(hc, "touch", err)
			}
			if err := f.Close(); err != nil {
				return fail(hc, "touch", err)
			}
			continue
		}
		if err := s.FS.Chtimes(path, now, now); err != nil {
			return fail(hc, "touch", err)
		}
	}
	return 0
}

func readLines(s *Shell, hc *interp.HandlerContext, args []string) ([]string, error) {
	var data []byte
	if len(args) == 0 {
		var err error
		data, err = io.ReadAll(hc.Stdin)
		if err != nil {
			return nil, err
		}
	} else {
		for _, arg := range args {
			b, err := afero.ReadFile(s.FS, resolveArg(hc, arg))
			if err != nil {
				return nil, err
			}
			data = append(data, b...)
		}
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	return lines, nil
}

func headTailCount(flags map[byte]bool, rest []string) (int, []string) {
	n := 10
	// support "-n 5" (parseFlags treats -n as flag; the count is the
	// first rest arg if numeric)
	if flags['n'] && len(rest) > 0 {
		if v, err := strconv.Atoi(rest[0]); err == nil {
			n = v
			rest = rest[1:]
		}
	}
	return n, rest
}

func runHead(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	flags, rest := parseFlags(args)
	n, rest := headTailCount(flags, rest)
	lines, err := readLines(s, hc, rest)
	if err != nil {
		return fail(hc, "head", err)
	}
	if n < len(lines) {
		lines = lines[:n]
	}
	for _, l := range lines {
		fprintln(hc.Stdout, l)
	}
	return 0
}

func runTail(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	flags, rest := parseFlags(args)
	n, rest := headTailCount(flags, rest)
	lines, err := readLines(s, hc, rest)
	if err != nil {
		return fail(hc, "tail", err)
	}
	if n < len(lines) {
		lines = lines[len(lines)-n:]
	}
	for _, l := range lines {
		fprintln(hc.Stdout, l)
	}
	return 0
}

func runWc(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	flags, rest := parseFlags(args)
	var data []byte
	var err error
	if len(rest) == 0 {
		data, err = io.ReadAll(hc.Stdin)
	} else {
		for _, arg := range rest {
			var b []byte
			b, err = afero.ReadFile(s.FS, resolveArg(hc, arg))
			if err != nil {
				break
			}
			data = append(data, b...)
		}
	}
	if err != nil {
		return fail(hc, "wc", err)
	}
	lines := strings.Count(string(data), "\n")
	words := len(strings.Fields(string(data)))
	bytes := len(data)
	if !flags['l'] && !flags['w'] && !flags['c'] {
		fprintf(hc.Stdout, "%7d %7d %7d\n", lines, words, bytes)
		return 0
	}
	var out []string
	if flags['l'] {
		out = append(out, strconv.Itoa(lines))
	}
	if flags['w'] {
		out = append(out, strconv.Itoa(words))
	}
	if flags['c'] {
		out = append(out, strconv.Itoa(bytes))
	}
	fprintln(hc.Stdout, strings.Join(out, " "))
	return 0
}

func runGrep(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	flags, rest := parseFlags(args)
	if len(rest) == 0 {
		fprintln(hc.Stderr, "grep: missing pattern")
		return 2
	}
	pattern := rest[0]
	if flags['i'] {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fail(hc, "grep", err)
	}
	lines, err := readLines(s, hc, rest[1:])
	if err != nil {
		return fail(hc, "grep", err)
	}
	count := 0
	for i, l := range lines {
		m := re.MatchString(l)
		if flags['v'] {
			m = !m
		}
		if m {
			count++
			if flags['c'] {
				continue
			}
			if flags['n'] {
				fprintf(hc.Stdout, "%d:%s\n", i+1, l)
			} else {
				fprintln(hc.Stdout, l)
			}
		}
	}
	if flags['c'] {
		fprintln(hc.Stdout, count)
	}
	if count == 0 {
		return 1
	}
	return 0
}

func runSeq(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	_, rest := parseFlags(args)
	first, incr, last := 1, 1, 1
	var err error
	switch len(rest) {
	case 1:
		last, err = strconv.Atoi(rest[0])
	case 2:
		first, err = strconv.Atoi(rest[0])
		if err == nil {
			last, err = strconv.Atoi(rest[1])
		}
	case 3:
		first, err = strconv.Atoi(rest[0])
		if err == nil {
			incr, err = strconv.Atoi(rest[1])
		}
		if err == nil {
			last, err = strconv.Atoi(rest[2])
		}
	default:
		fprintln(hc.Stderr, "usage: seq [first [incr]] last")
		return 1
	}
	if err != nil || incr == 0 {
		fprintln(hc.Stderr, "seq: invalid arguments")
		return 1
	}
	for i := first; (incr > 0 && i <= last) || (incr < 0 && i >= last); i += incr {
		fprintln(hc.Stdout, i)
	}
	return 0
}

func runSort(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	flags, rest := parseFlags(args)
	lines, err := readLines(s, hc, rest)
	if err != nil {
		return fail(hc, "sort", err)
	}
	if flags['n'] {
		sort.SliceStable(lines, func(i, j int) bool {
			return numericKey(lines[i]) < numericKey(lines[j])
		})
	} else {
		sort.Strings(lines)
	}
	if flags['r'] {
		for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
			lines[i], lines[j] = lines[j], lines[i]
		}
	}
	for _, l := range lines {
		fprintln(hc.Stdout, l)
	}
	return 0
}

func runUniq(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	flags, rest := parseFlags(args)
	lines, err := readLines(s, hc, rest)
	if err != nil {
		return fail(hc, "uniq", err)
	}
	var prev string
	count := 0
	flush := func() {
		if count == 0 {
			return
		}
		if flags['c'] {
			fprintf(hc.Stdout, "%7d %s\n", count, prev)
		} else {
			fprintln(hc.Stdout, prev)
		}
	}
	for _, l := range lines {
		if count > 0 && l == prev {
			count++
			continue
		}
		flush()
		prev = l
		count = 1
	}
	flush()
	return 0
}

func runTree(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	_, rest := parseFlags(args)
	root := "."
	if len(rest) > 0 {
		root = rest[0]
	}
	path := resolveArg(hc, root)
	fprintln(hc.Stdout, root)
	var walk func(dir, prefix string) error
	walk = func(dir, prefix string) error {
		infos, err := afero.ReadDir(s.FS, dir)
		if err != nil {
			return err
		}
		for i, fi := range infos {
			connector, childPrefix := "├── ", "│   "
			if i == len(infos)-1 {
				connector, childPrefix = "└── ", "    "
			}
			fprintln(hc.Stdout, prefix+connector+fi.Name())
			if fi.IsDir() {
				if err := walk(filepath.Join(dir, fi.Name()), prefix+childPrefix); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(path, ""); err != nil {
		return fail(hc, "tree", err)
	}
	return 0
}

func runBasename(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	if len(args) == 0 {
		fprintln(hc.Stderr, "basename: missing operand")
		return 1
	}
	fprintln(hc.Stdout, filepath.Base(args[0]))
	return 0
}

func runDirname(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	if len(args) == 0 {
		fprintln(hc.Stderr, "dirname: missing operand")
		return 1
	}
	fprintln(hc.Stdout, filepath.Dir(args[0]))
	return 0
}

func runDate(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	fprintln(hc.Stdout, time.Now().Format("Mon Jan  2 15:04:05 MST 2006"))
	return 0
}

func runSleep(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	if len(args) == 0 {
		fprintln(hc.Stderr, "sleep: missing operand")
		return 1
	}
	secs, err := strconv.ParseFloat(args[0], 64)
	if err != nil {
		return fail(hc, "sleep", err)
	}
	select {
	case <-time.After(time.Duration(secs * float64(time.Second))):
		return 0
	case <-ctx.Done():
		return 130
	}
}

func runClear(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	fprint(hc.Stdout, "\x1b[2J\x1b[H")
	return 0
}

func runEnv(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	hc.Env.Each(func(name string, vr expand.Variable) bool {
		if vr.Exported {
			fprintf(hc.Stdout, "%s=%s\n", name, vr.String())
		}
		return true
	})
	return 0
}

func runWhich(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	code := 0
	for _, arg := range args {
		if _, ok := applets[arg]; ok {
			fprintf(hc.Stdout, "/bin/%s\n", arg)
		} else {
			fprintf(hc.Stderr, "which: %s not found\n", arg)
			code = 1
		}
	}
	return code
}

func runUname(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	fprintln(hc.Stdout, "websh wasm")
	return 0
}

func runHostname(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	fprintln(hc.Stdout, "websh")
	return 0
}

func runHelp(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	names := make([]string, 0, len(applets))
	for name := range applets {
		names = append(names, name)
	}
	sort.Strings(names)
	fprintln(hc.Stdout, "websh applets:")
	for _, name := range names {
		fprintf(hc.Stdout, "  %-10s %s\n", name, applets[name].help)
	}
	fprintln(hc.Stdout, "plus the shell builtins: cd pwd echo printf read exit export unset source test [ ...")
	fprintln(hc.Stdout, "for those, bash-style: \x1b[1mbuiltin help\x1b[0m  (or \x1b[1mbuiltin help cd\x1b[0m for one)")
	return 0
}
