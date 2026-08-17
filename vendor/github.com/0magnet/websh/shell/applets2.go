package shell

// Second wave of applets: text tools, hashing and filesystem
// inspection. All written against afero like the first wave.

import (
	"context"
	// #nosec G501 -- md5sum(1) is being implemented here. The digest is the
	// command's output, not a security control; a stronger hash would make
	// the applet produce wrong answers.
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/0magnet/afero"
	"github.com/0magnet/sh/v3/interp"
)

func init() {
	applets["find"] = applet{"walk directories (-name pattern, -type f|d)", runFind}
	applets["cut"] = applet{"select fields (-d delim -f list) or chars (-c list)", runCut}
	applets["tr"] = applet{"translate characters (tr a-z A-Z; -d set deletes)", runTr}
	applets["sed"] = applet{"stream edit: sed s/regex/replacement/[gi]", runSed}
	applets["xargs"] = applet{"build command lines from stdin", runXargs}
	applets["tac"] = applet{"reverse lines", runTac}
	applets["nl"] = applet{"number lines", runNl}
	applets["du"] = applet{"disk usage (-s summary)", runDu}
	applets["stat"] = applet{"file status", runStat}
	applets["chmod"] = applet{"change file mode (octal)", runChmod}
	applets["md5sum"] = applet{"MD5 digests", hashApplet("md5sum")}
	applets["sha256sum"] = applet{"SHA256 digests", hashApplet("sha256sum")}
	applets["base64"] = applet{"base64 encode (-d decode)", runBase64}
	applets["xxd"] = applet{"hex dump", runXxd}
}

func runFind(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	root := "."
	namePat := ""
	typeFilter := byte(0)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-name":
			if i+1 < len(args) {
				i++
				namePat = args[i]
			}
		case "-type":
			if i+1 < len(args) {
				i++
				if len(args[i]) > 0 {
					typeFilter = args[i][0]
				}
			}
		default:
			if !strings.HasPrefix(args[i], "-") {
				root = args[i]
			}
		}
	}
	base := resolveArg(hc, root)
	code := 0
	err := afero.Walk(s.FS, base, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if typeFilter == 'f' && info.IsDir() {
			return nil
		}
		if typeFilter == 'd' && !info.IsDir() {
			return nil
		}
		if namePat != "" {
			ok, err := path.Match(namePat, info.Name())
			if err != nil {
				return err // a malformed pattern is worth reporting, not ignoring
			}
			if !ok {
				return nil
			}
		}
		// print relative to the argument like find does
		rel := p
		if r, err := filepath.Rel(base, p); err == nil {
			if r == "." {
				rel = root
			} else {
				rel = filepath.Join(root, r)
			}
		}
		fprintln(hc.Stdout, rel)
		return nil
	})
	if err != nil {
		code = fail(hc, "find", err)
	}
	return code
}

// parseFieldList parses "1,3-5" into a selector.
func parseFieldList(spec string) func(int) bool {
	type rng struct{ lo, hi int }
	var ranges []rng
	for _, part := range strings.Split(spec, ",") {
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			// open ends: "-5" means 1-5, "6-" means 6 to the end
			l, err1 := strconv.Atoi(lo)
			if lo == "" {
				l, err1 = 1, nil
			}
			h, err2 := strconv.Atoi(hi)
			if hi == "" {
				h, err2 = int(^uint(0)>>1), nil
			}
			if err1 == nil && err2 == nil {
				ranges = append(ranges, rng{l, h})
			}
		} else if v, err := strconv.Atoi(part); err == nil {
			ranges = append(ranges, rng{v, v})
		}
	}
	return func(n int) bool {
		for _, r := range ranges {
			if n >= r.lo && n <= r.hi {
				return true
			}
		}
		return false
	}
}

func runCut(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	delim := "\t"
	fieldSpec, charSpec := "", ""
	var files []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-d" && i+1 < len(args):
			i++
			delim = args[i]
		case strings.HasPrefix(args[i], "-d") && len(args[i]) > 2:
			delim = args[i][2:]
		case args[i] == "-f" && i+1 < len(args):
			i++
			fieldSpec = args[i]
		case strings.HasPrefix(args[i], "-f") && len(args[i]) > 2:
			fieldSpec = args[i][2:]
		case args[i] == "-c" && i+1 < len(args):
			i++
			charSpec = args[i]
		case strings.HasPrefix(args[i], "-c") && len(args[i]) > 2:
			charSpec = args[i][2:]
		default:
			files = append(files, args[i])
		}
	}
	if fieldSpec == "" && charSpec == "" {
		fprintln(hc.Stderr, "cut: specify -f or -c")
		return 1
	}
	lines, err := readLines(s, hc, files)
	if err != nil {
		return fail(hc, "cut", err)
	}
	if charSpec != "" {
		want := parseFieldList(charSpec)
		for _, l := range lines {
			var b strings.Builder
			for i, r := range []rune(l) {
				if want(i + 1) {
					b.WriteRune(r)
				}
			}
			fprintln(hc.Stdout, b.String())
		}
		return 0
	}
	want := parseFieldList(fieldSpec)
	for _, l := range lines {
		if !strings.Contains(l, delim) {
			fprintln(hc.Stdout, l)
			continue
		}
		parts := strings.Split(l, delim)
		var out []string
		for i, p := range parts {
			if want(i + 1) {
				out = append(out, p)
			}
		}
		fprintln(hc.Stdout, strings.Join(out, delim))
	}
	return 0
}

// expandTrSet expands ranges like a-z into the full rune set.
func expandTrSet(s string) []rune {
	var out []rune
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if i+2 < len(runes) && runes[i+1] == '-' && runes[i+2] >= runes[i] {
			for r := runes[i]; r <= runes[i+2]; r++ {
				out = append(out, r)
			}
			i += 2
			continue
		}
		out = append(out, runes[i])
	}
	return out
}

func runTr(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	flags, rest := parseFlags(args)
	if len(rest) == 0 {
		fprintln(hc.Stderr, "usage: tr [-d] set1 [set2]")
		return 1
	}
	data, err := io.ReadAll(hc.Stdin)
	if err != nil {
		return fail(hc, "tr", err)
	}
	set1 := expandTrSet(rest[0])
	if flags['d'] {
		del := map[rune]bool{}
		for _, r := range set1 {
			del[r] = true
		}
		var b strings.Builder
		for _, r := range string(data) {
			if !del[r] {
				b.WriteRune(r)
			}
		}
		fprint(hc.Stdout, b.String())
		return 0
	}
	if len(rest) < 2 {
		fprintln(hc.Stderr, "tr: missing set2")
		return 1
	}
	set2 := expandTrSet(rest[1])
	m := map[rune]rune{}
	for i, r := range set1 {
		j := i
		if j >= len(set2) {
			j = len(set2) - 1
		}
		if j >= 0 {
			m[r] = set2[j]
		}
	}
	var b strings.Builder
	for _, r := range string(data) {
		if repl, ok := m[r]; ok {
			b.WriteRune(repl)
		} else {
			b.WriteRune(r)
		}
	}
	fprint(hc.Stdout, b.String())
	return 0
}

func runSed(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	flags, rest := parseFlags(args)
	_ = flags
	if len(rest) == 0 {
		fprintln(hc.Stderr, "usage: sed s/regex/replacement/[gi] [file...]")
		return 1
	}
	script := rest[0]
	if len(script) < 4 || script[0] != 's' {
		fprintln(hc.Stderr, "sed: only s/// scripts are supported")
		return 1
	}
	delim := string(script[1])
	parts := strings.Split(script[2:], delim)
	if len(parts) < 2 {
		fprintln(hc.Stderr, "sed: malformed s command")
		return 1
	}
	pattern, replacement := parts[0], parts[1]
	mods := ""
	if len(parts) > 2 {
		mods = parts[2]
	}
	if strings.Contains(mods, "i") {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fail(hc, "sed", err)
	}
	global := strings.Contains(mods, "g")
	// sed uses \1 for group references; Go uses $1
	replacement = regexp.MustCompile(`\\(\d)`).ReplaceAllString(replacement, "$$$1")

	lines, err := readLines(s, hc, rest[1:])
	if err != nil {
		return fail(hc, "sed", err)
	}
	for _, l := range lines {
		if global {
			l = re.ReplaceAllString(l, replacement)
		} else if loc := re.FindStringIndex(l); loc != nil {
			l = l[:loc[0]] + re.ReplaceAllString(l[loc[0]:loc[1]], replacement) + l[loc[1]:]
		}
		fprintln(hc.Stdout, l)
	}
	return 0
}

func runXargs(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	data, err := io.ReadAll(hc.Stdin)
	if err != nil {
		return fail(hc, "xargs", err)
	}
	extra := strings.Fields(string(data))
	cmd := []string{"echo"}
	if len(args) > 0 {
		cmd = args
	}
	full := append(append([]string{}, cmd[1:]...), extra...)
	name := cmd[0]
	// echo is an interpreter builtin, not an applet: handle inline
	if name == "echo" {
		fprintln(hc.Stdout, strings.Join(full, " "))
		return 0
	}
	if a, ok := applets[name]; ok {
		return a.run(ctx, s, hc, full)
	}
	fprintf(hc.Stderr, "xargs: %s: not an applet (xargs can only run applets and echo)\n", name)
	return 127
}

func runTac(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	_, rest := parseFlags(args)
	lines, err := readLines(s, hc, rest)
	if err != nil {
		return fail(hc, "tac", err)
	}
	for i := len(lines) - 1; i >= 0; i-- {
		fprintln(hc.Stdout, lines[i])
	}
	return 0
}

func runNl(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	_, rest := parseFlags(args)
	lines, err := readLines(s, hc, rest)
	if err != nil {
		return fail(hc, "nl", err)
	}
	for i, l := range lines {
		fprintf(hc.Stdout, "%6d\t%s\n", i+1, l)
	}
	return 0
}

func runDu(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	flags, rest := parseFlags(args)
	root := "."
	if len(rest) > 0 {
		root = rest[0]
	}
	base := resolveArg(hc, root)
	dirTotals := map[string]int64{}
	var grand int64
	walkErr := afero.Walk(s.FS, base, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			dirTotals[filepath.Dir(p)] += info.Size()
			grand += info.Size()
		} else if _, ok := dirTotals[p]; !ok {
			dirTotals[p] = 0
		}
		return nil
	})
	if walkErr != nil {
		return fail(hc, "du", walkErr)
	}
	if flags['s'] {
		fprintf(hc.Stdout, "%d\t%s\n", grand, root)
		return 0
	}
	for dir, size := range dirTotals {
		rel := dir
		if r, err := filepath.Rel(base, dir); err == nil && r != "." {
			rel = filepath.Join(root, r)
		} else if r == "." {
			rel = root
		}
		fprintf(hc.Stdout, "%d\t%s\n", size, rel)
	}
	return 0
}

func runStat(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	_, rest := parseFlags(args)
	if len(rest) == 0 {
		fprintln(hc.Stderr, "stat: missing operand")
		return 1
	}
	code := 0
	for _, arg := range rest {
		info, err := s.FS.Stat(resolveArg(hc, arg))
		if err != nil {
			code = fail(hc, "stat", err)
			continue
		}
		kind := "regular file"
		if info.IsDir() {
			kind = "directory"
		}
		fprintf(hc.Stdout, "  File: %s\n  Size: %d\t%s\n  Mode: %s\nModify: %s\n",
			arg, info.Size(), kind, info.Mode(), info.ModTime().Format("2006-01-02 15:04:05"))
	}
	return code
}

func runChmod(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	_, rest := parseFlags(args)
	if len(rest) < 2 {
		fprintln(hc.Stderr, "usage: chmod octal-mode file...")
		return 1
	}
	mode, err := strconv.ParseUint(rest[0], 8, 32)
	if err != nil {
		return fail(hc, "chmod", err)
	}
	for _, arg := range rest[1:] {
		if err := s.FS.Chmod(resolveArg(hc, arg), os.FileMode(mode)); err != nil {
			return fail(hc, "chmod", err)
		}
	}
	return 0
}

func hashApplet(name string) func(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	return func(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
		_, rest := parseFlags(args)
		digest := func(data []byte) string {
			if name == "md5sum" {
				sum := md5.Sum(data) // #nosec G401 -- see the crypto/md5 import comment
				return hex.EncodeToString(sum[:])
			}
			sum := sha256.Sum256(data)
			return hex.EncodeToString(sum[:])
		}
		if len(rest) == 0 {
			data, err := io.ReadAll(hc.Stdin)
			if err != nil {
				return fail(hc, name, err)
			}
			fprintf(hc.Stdout, "%s  -\n", digest(data))
			return 0
		}
		code := 0
		for _, arg := range rest {
			data, err := afero.ReadFile(s.FS, resolveArg(hc, arg))
			if err != nil {
				code = fail(hc, name, err)
				continue
			}
			fprintf(hc.Stdout, "%s  %s\n", digest(data), arg)
		}
		return code
	}
}

func runBase64(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	flags, rest := parseFlags(args)
	var data []byte
	var err error
	if len(rest) == 0 {
		data, err = io.ReadAll(hc.Stdin)
	} else {
		data, err = afero.ReadFile(s.FS, resolveArg(hc, rest[0]))
	}
	if err != nil {
		return fail(hc, "base64", err)
	}
	if flags['d'] {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if err != nil {
			return fail(hc, "base64", err)
		}
		write(hc.Stdout, decoded)
		return 0
	}
	fprintln(hc.Stdout, base64.StdEncoding.EncodeToString(data))
	return 0
}

func runXxd(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	_, rest := parseFlags(args)
	var data []byte
	var err error
	if len(rest) == 0 {
		data, err = io.ReadAll(hc.Stdin)
	} else {
		data, err = afero.ReadFile(s.FS, resolveArg(hc, rest[0]))
	}
	if err != nil {
		return fail(hc, "xxd", err)
	}
	for off := 0; off < len(data); off += 16 {
		end := off + 16
		if end > len(data) {
			end = len(data)
		}
		chunk := data[off:end]
		var hexPart strings.Builder
		for i := 0; i < 16; i++ {
			if i < len(chunk) {
				fprintf(&hexPart, "%02x", chunk[i])
			} else {
				hexPart.WriteString("  ")
			}
			if i%2 == 1 {
				hexPart.WriteString(" ")
			}
		}
		var ascii strings.Builder
		for _, b := range chunk {
			if b >= 32 && b < 127 {
				ascii.WriteByte(b)
			} else {
				ascii.WriteByte('.')
			}
		}
		fprintf(hc.Stdout, "%08x: %s %s\n", off, hexPart.String(), ascii.String())
	}
	return 0
}
