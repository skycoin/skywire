// Package logging pkg/logging/formatter_golden_test.go c0-com-log
package logging

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/formatter_golden.txt from the current formatter output")

const goldenFile = "testdata/formatter_golden.txt"

// goldenTime is fixed and in UTC so the rendered timestamps do not depend on
// the machine's clock or zone.
var goldenTime = time.Date(2026, 3, 4, 5, 6, 7, 890123456, time.UTC)

// prodFormatter mirrors NewMasterLogger's formatter, which is what every
// skywire process actually runs with.
func prodFormatter() *TextFormatter {
	return &TextFormatter{
		FullTimestamp:      true,
		AlwaysQuoteStrings: true,
		QuoteEmptyFields:   true,
		ForceFormatting:    true,
		DisableColors:      false,
		ForceColors:        false,
		TimestampFormat:    "2006-01-02T15:04:05.0000Z07:00",
	}
}

func coloredFormatter() *TextFormatter {
	f := prodFormatter()
	f.ForceColors = true
	return f
}

func goldenEntry(level Level, msg string, data Fields) *Entry {
	if data == nil {
		data = Fields{}
	}
	return &Entry{
		Logger:  &MasterLogger{Out: io.Discard, Formatter: &TextFormatter{}},
		Data:    data,
		Time:    goldenTime,
		Level:   level,
		Message: msg,
	}
}

func moduleFields(module string, extra Fields) Fields {
	d := Fields{logModuleKey: module}
	maps.Copy(d, extra)
	return d
}

type goldenCase struct {
	name  string
	newf  func() *TextFormatter
	entry *Entry
}

func goldenCases() []goldenCase {
	cases := []goldenCase{
		{"plain/no-fields", prodFormatter, goldenEntry(InfoLevel, "serving", moduleFields("dmsg_server", nil))},
		{"plain/empty-message", prodFormatter, goldenEntry(InfoLevel, "", moduleFields("dmsg_server", nil))},
		{"plain/no-module", prodFormatter, goldenEntry(InfoLevel, "serving", nil)},
		{"plain/one-field", prodFormatter, goldenEntry(InfoLevel, "stream closed", moduleFields("dmsg_server", Fields{"dst": "abc"}))},
		{"plain/three-fields", prodFormatter, goldenEntry(InfoLevel, "stream closed", moduleFields("dmsg_server", Fields{"dst": "abc", "src": "def", "size": 4096}))},
		{"plain/unsorted-keys", prodFormatter, goldenEntry(InfoLevel, "z first", moduleFields("dmsg_server", Fields{"zulu": 1, "alpha": 2, "mike": 3}))},
		{"plain/error-field", prodFormatter, goldenEntry(ErrorLevel, "dial failed", moduleFields("dmsg_server", Fields{ErrorKey: errors.New("connection refused")}))},
		{"plain/error-field-plain-word", prodFormatter, goldenEntry(ErrorLevel, "dial failed", moduleFields("dmsg_server", Fields{ErrorKey: errors.New("refused")}))},
		{"plain/message-with-spaces-and-quotes", prodFormatter, goldenEntry(InfoLevel, `he said "hi there" & left`, moduleFields("dmsg_server", nil))},
		{"plain/value-with-quotes", prodFormatter, goldenEntry(InfoLevel, "quoting", moduleFields("dmsg_server", Fields{"raw": `a "b" c`}))},
		{"plain/empty-value", prodFormatter, goldenEntry(InfoLevel, "empty", moduleFields("dmsg_server", Fields{"empty": ""}))},
		{"plain/bool-and-float-values", prodFormatter, goldenEntry(InfoLevel, "typed", moduleFields("dmsg_server", Fields{"ok": true, "ratio": 1.5, "n": int64(-7)}))},
		{"plain/struct-value", prodFormatter, goldenEntry(InfoLevel, "typed", moduleFields("dmsg_server", Fields{"pt": struct {
			X int
			Y string
		}{1, "two"}}))},
		{"plain/nil-value", prodFormatter, goldenEntry(InfoLevel, "typed", moduleFields("dmsg_server", Fields{"nothing": nil}))},
		{"plain/module-with-colon", prodFormatter, goldenEntry(InfoLevel, "serving", moduleFields("dmsgC:disc:cxo-lookup", nil))},
		{"plain/empty-module", prodFormatter, goldenEntry(InfoLevel, "serving", moduleFields("", nil))},
		{"plain/skipped-prefix-key", prodFormatter, goldenEntry(InfoLevel, "serving", moduleFields("dmsg_server", Fields{"prefix": "ignored", "kept": "yes"}))},
		{"plain/many-fields", prodFormatter, goldenEntry(InfoLevel, "many", moduleFields("dmsg_server", Fields{
			"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "f": 6, "g": 7, "h": 8, "i": 9, "j": 10, "k": 11, "l": 12,
		}))},
		{"plain/exactly-eight-keys", prodFormatter, goldenEntry(InfoLevel, "eight", moduleFields("dmsg_server", Fields{
			"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "f": 6, "g": 7,
		}))},
		{"colored/many-fields", coloredFormatter, goldenEntry(InfoLevel, "many", moduleFields("dmsg_server", Fields{
			"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "f": 6, "g": 7, "h": 8, "i": 9, "j": 10, "k": 11, "l": 12,
		}))},
	}

	for _, lvl := range []Level{TraceLevel, DebugLevel, InfoLevel, WarnLevel, ErrorLevel, FatalLevel, PanicLevel} {
		cases = append(cases,
			goldenCase{"level/plain/" + lvl.String(), prodFormatter, goldenEntry(lvl, "level check", moduleFields("dmsg_server", Fields{"k": "v"}))},
			goldenCase{"level/colored/" + lvl.String(), coloredFormatter, goldenEntry(lvl, "level check", moduleFields("dmsg_server", Fields{"k": "v"}))},
		)
	}

	cases = append(cases,
		goldenCase{"colored/no-fields", coloredFormatter, goldenEntry(InfoLevel, "serving", moduleFields("dmsg_server", nil))},
		goldenCase{"colored/no-module", coloredFormatter, goldenEntry(InfoLevel, "serving", nil)},
		goldenCase{"colored/three-fields", coloredFormatter, goldenEntry(InfoLevel, "stream closed", moduleFields("dmsg_server", Fields{"dst": "abc", "src": "def", "size": 4096}))},
		goldenCase{"colored/error-field", coloredFormatter, goldenEntry(ErrorLevel, "dial failed", moduleFields("dmsg_server", Fields{ErrorKey: errors.New("connection refused")}))},

		goldenCase{"critical/plain", prodFormatter, goldenEntry(ErrorLevel, "the sky is falling", Fields{logModuleKey: "dmsg_server", logPriorityKey: logPriorityCritical})},
		goldenCase{"critical/colored", coloredFormatter, goldenEntry(ErrorLevel, "the sky is falling", Fields{logModuleKey: "dmsg_server", logPriorityKey: logPriorityCritical})},
		goldenCase{"critical/colored-with-fields", coloredFormatter, goldenEntry(ErrorLevel, "the sky is falling", Fields{logModuleKey: "dmsg_server", logPriorityKey: logPriorityCritical, "k": "v"})},
		goldenCase{"critical/no-module", coloredFormatter, goldenEntry(ErrorLevel, "the sky is falling", Fields{logPriorityKey: logPriorityCritical})},
		goldenCase{"critical/non-critical-priority", coloredFormatter, goldenEntry(ErrorLevel, "odd priority", Fields{logModuleKey: "dmsg_server", logPriorityKey: "LOW"})},

		goldenCase{"callcontext/file-only", coloredFormatter, goldenEntry(InfoLevel, "cc", moduleFields("dmsg_server", Fields{"file": "server.go"}))},
		goldenCase{"callcontext/func-only", coloredFormatter, goldenEntry(InfoLevel, "cc", moduleFields("dmsg_server", Fields{"func": "Serve"}))},
		goldenCase{"callcontext/line-int", coloredFormatter, goldenEntry(InfoLevel, "cc", moduleFields("dmsg_server", Fields{"line": 42}))},
		goldenCase{"callcontext/line-string", coloredFormatter, goldenEntry(InfoLevel, "cc", moduleFields("dmsg_server", Fields{"line": "42"}))},
		goldenCase{"callcontext/line-uint64", coloredFormatter, goldenEntry(InfoLevel, "cc", moduleFields("dmsg_server", Fields{"line": uint64(42)}))},
		goldenCase{"callcontext/line-float-ignored", coloredFormatter, goldenEntry(InfoLevel, "cc", moduleFields("dmsg_server", Fields{"line": 42.5}))},
		goldenCase{"callcontext/all-three", coloredFormatter, goldenEntry(InfoLevel, "cc", moduleFields("dmsg_server", Fields{"file": "server.go", "func": "Serve", "line": 42}))},
		goldenCase{"callcontext/all-three-plain", prodFormatter, goldenEntry(InfoLevel, "cc", moduleFields("dmsg_server", Fields{"file": "server.go", "func": "Serve", "line": 42}))},
		goldenCase{"callcontext/empty-strings", coloredFormatter, goldenEntry(InfoLevel, "cc", moduleFields("dmsg_server", Fields{"file": "", "func": "", "line": ""}))},
		goldenCase{"callcontext/wrong-types", coloredFormatter, goldenEntry(InfoLevel, "cc", moduleFields("dmsg_server", Fields{"file": 7, "func": true}))},
		goldenCase{"callcontext/critical", coloredFormatter, goldenEntry(ErrorLevel, "cc", Fields{logModuleKey: "dmsg_server", logPriorityKey: logPriorityCritical, "file": "server.go", "line": 42})},

		goldenCase{"opt/no-timestamp", func() *TextFormatter {
			f := coloredFormatter()
			f.DisableTimestamp = true
			return f
		}, goldenEntry(InfoLevel, "no ts", moduleFields("dmsg_server", Fields{"k": "v"}))},
		goldenCase{"opt/no-timestamp-critical", func() *TextFormatter {
			f := coloredFormatter()
			f.DisableTimestamp = true
			return f
		}, goldenEntry(ErrorLevel, "no ts", Fields{logModuleKey: "dmsg_server", logPriorityKey: logPriorityCritical})},
		goldenCase{"opt/mini-timestamp", func() *TextFormatter {
			f := coloredFormatter()
			f.FullTimestamp = false
			return f
		}, goldenEntry(InfoLevel, "mini ts", moduleFields("dmsg_server", nil))},
		goldenCase{"opt/mini-timestamp-critical", func() *TextFormatter {
			f := coloredFormatter()
			f.FullTimestamp = false
			return f
		}, goldenEntry(InfoLevel, "mini ts", Fields{logModuleKey: "dmsg_server", logPriorityKey: logPriorityCritical})},
		goldenCase{"opt/default-timestamp-format", func() *TextFormatter {
			f := coloredFormatter()
			f.TimestampFormat = ""
			return f
		}, goldenEntry(InfoLevel, "rfc3339", moduleFields("dmsg_server", nil))},
		goldenCase{"opt/no-uppercase", func() *TextFormatter {
			f := coloredFormatter()
			f.DisableUppercase = true
			return f
		}, goldenEntry(WarnLevel, "lowercase level", moduleFields("dmsg_server", nil))},
		goldenCase{"opt/no-sorting", func() *TextFormatter {
			f := prodFormatter()
			f.DisableSorting = true
			return f
		}, goldenEntry(InfoLevel, "unsorted", moduleFields("dmsg_server", Fields{"only": "one"}))},
		goldenCase{"opt/space-padding", func() *TextFormatter {
			f := coloredFormatter()
			f.SpacePadding = 30
			return f
		}, goldenEntry(InfoLevel, "padded", moduleFields("dmsg_server", Fields{"k": "v"}))},
		goldenCase{"opt/space-padding-empty-message", func() *TextFormatter {
			f := coloredFormatter()
			f.SpacePadding = 10
			return f
		}, goldenEntry(InfoLevel, "", moduleFields("dmsg_server", nil))},
		goldenCase{"opt/space-padding-overflow", func() *TextFormatter {
			f := coloredFormatter()
			f.SpacePadding = 3
			return f
		}, goldenEntry(InfoLevel, "a much longer message", moduleFields("dmsg_server", nil))},
		goldenCase{"opt/space-padding-multibyte", func() *TextFormatter {
			f := coloredFormatter()
			f.SpacePadding = 12
			return f
		}, goldenEntry(InfoLevel, "héllo wörld", moduleFields("dmsg_server", nil))},
		goldenCase{"opt/space-padding-critical", func() *TextFormatter {
			f := coloredFormatter()
			f.SpacePadding = 30
			return f
		}, goldenEntry(ErrorLevel, "padded", Fields{logModuleKey: "dmsg_server", logPriorityKey: logPriorityCritical})},
		goldenCase{"opt/no-quoting", func() *TextFormatter {
			f := coloredFormatter()
			f.AlwaysQuoteStrings = false
			f.QuoteEmptyFields = false
			return f
		}, goldenEntry(InfoLevel, "quotes off", moduleFields("dmsg_server", Fields{"plain": "abc-1.2", "spaced": "a b", "empty": "", "err": errors.New("a b"), "errplain": errors.New("ab")}))},
		goldenCase{"opt/custom-quote-character", func() *TextFormatter {
			f := coloredFormatter()
			f.QuoteCharacter = "'"
			return f
		}, goldenEntry(InfoLevel, "custom quote", moduleFields("dmsg_server", Fields{"k": "v"}))},
		goldenCase{"opt/disable-colors-wins", func() *TextFormatter {
			f := coloredFormatter()
			f.DisableColors = true
			return f
		}, goldenEntry(InfoLevel, "no colors", moduleFields("dmsg_server", Fields{"k": "v"}))},
		goldenCase{"opt/custom-color-scheme", func() *TextFormatter {
			f := coloredFormatter()
			f.SetColorScheme(&ColorScheme{InfoLevelStyle: "magenta", PrefixStyle: "red+h"})
			return f
		}, goldenEntry(InfoLevel, "custom scheme", moduleFields("dmsg_server", Fields{"k": "v"}))},

		goldenCase{"unformatted/no-fields", func() *TextFormatter {
			f := prodFormatter()
			f.ForceFormatting = false
			return f
		}, goldenEntry(InfoLevel, "serving", moduleFields("dmsg_server", nil))},
		goldenCase{"unformatted/fields", func() *TextFormatter {
			f := prodFormatter()
			f.ForceFormatting = false
			return f
		}, goldenEntry(InfoLevel, "serving", moduleFields("dmsg_server", Fields{"dst": "abc", "n": 3}))},
		goldenCase{"unformatted/no-timestamp-no-message", func() *TextFormatter {
			f := prodFormatter()
			f.ForceFormatting = false
			f.DisableTimestamp = true
			return f
		}, goldenEntry(InfoLevel, "", moduleFields("dmsg_server", Fields{"dst": "abc"}))},
	)

	return cases
}

// render is the formatter under test, driven exactly as logrus drives it.
func render(t *testing.T, f *TextFormatter, entry *Entry) string {
	t.Helper()
	out, err := f.Format(entry)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	return string(out)
}

// TestFormatterGolden pins the formatter's byte-for-byte output, colors
// included, so the rendering can be rewritten for speed without changing a
// single byte of what operators read.
func TestFormatterGolden(t *testing.T) {
	// miniTS reads the package clock; pin it so the [%04d] cases are stable.
	baseTimestamp = time.Now().Add(-42*time.Second - 500*time.Millisecond)

	cases := goldenCases()

	if *updateGolden {
		var sb strings.Builder
		for _, c := range cases {
			fmt.Fprintf(&sb, "%s\n%s\n", c.name, strconv.Quote(render(t, c.newf(), c.entry)))
		}
		if err := os.WriteFile(goldenFile, []byte(sb.String()), 0o600); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("wrote %d cases to %s", len(cases), goldenFile)
		return
	}

	want, err := readGolden()
	if err != nil {
		t.Fatalf("read golden (run 'go test ./pkg/logging/ -run TestFormatterGolden -update'): %v", err)
	}

	seen := make(map[string]bool, len(cases))
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if seen[c.name] {
				t.Fatalf("duplicate golden case name %q", c.name)
			}
			seen[c.name] = true

			expected, ok := want[c.name]
			if !ok {
				t.Fatalf("no golden entry for %q", c.name)
			}
			got := render(t, c.newf(), c.entry)
			if got != expected {
				t.Errorf("output changed\n want: %s\n  got: %s", strconv.Quote(expected), strconv.Quote(got))
			}
		})
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("golden entry %q has no matching case", name)
		}
	}
}

func readGolden() (map[string]string, error) {
	fh, err := os.Open(goldenFile)
	if err != nil {
		return nil, err
	}
	defer fh.Close() //nolint:errcheck

	out := make(map[string]string)
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		name := sc.Text()
		if !sc.Scan() {
			return nil, fmt.Errorf("golden entry %q has no value line", name)
		}
		v, err := strconv.Unquote(sc.Text())
		if err != nil {
			return nil, fmt.Errorf("golden entry %q: %w", name, err)
		}
		out[name] = v
	}
	return out, sc.Err()
}
