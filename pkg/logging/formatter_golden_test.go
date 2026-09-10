// Package logging pkg/logging/formatter_golden_test.go c0-com-log
package logging

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
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

func goldenEntry(level logrus.Level, msg string, data logrus.Fields) *logrus.Entry {
	if data == nil {
		data = logrus.Fields{}
	}
	return &logrus.Entry{
		Logger:  &logrus.Logger{Out: io.Discard, Formatter: &logrus.TextFormatter{}, Level: logrus.TraceLevel},
		Data:    data,
		Time:    goldenTime,
		Level:   level,
		Message: msg,
	}
}

func moduleFields(module string, extra logrus.Fields) logrus.Fields {
	d := logrus.Fields{logModuleKey: module}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

type goldenCase struct {
	name  string
	newf  func() *TextFormatter
	entry *logrus.Entry
}

func goldenCases() []goldenCase {
	cases := []goldenCase{
		{"plain/no-fields", prodFormatter, goldenEntry(logrus.InfoLevel, "serving", moduleFields("dmsg_server", nil))},
		{"plain/empty-message", prodFormatter, goldenEntry(logrus.InfoLevel, "", moduleFields("dmsg_server", nil))},
		{"plain/no-module", prodFormatter, goldenEntry(logrus.InfoLevel, "serving", nil)},
		{"plain/one-field", prodFormatter, goldenEntry(logrus.InfoLevel, "stream closed", moduleFields("dmsg_server", logrus.Fields{"dst": "abc"}))},
		{"plain/three-fields", prodFormatter, goldenEntry(logrus.InfoLevel, "stream closed", moduleFields("dmsg_server", logrus.Fields{"dst": "abc", "src": "def", "size": 4096}))},
		{"plain/unsorted-keys", prodFormatter, goldenEntry(logrus.InfoLevel, "z first", moduleFields("dmsg_server", logrus.Fields{"zulu": 1, "alpha": 2, "mike": 3}))},
		{"plain/error-field", prodFormatter, goldenEntry(logrus.ErrorLevel, "dial failed", moduleFields("dmsg_server", logrus.Fields{logrus.ErrorKey: errors.New("connection refused")}))},
		{"plain/error-field-plain-word", prodFormatter, goldenEntry(logrus.ErrorLevel, "dial failed", moduleFields("dmsg_server", logrus.Fields{logrus.ErrorKey: errors.New("refused")}))},
		{"plain/message-with-spaces-and-quotes", prodFormatter, goldenEntry(logrus.InfoLevel, `he said "hi there" & left`, moduleFields("dmsg_server", nil))},
		{"plain/value-with-quotes", prodFormatter, goldenEntry(logrus.InfoLevel, "quoting", moduleFields("dmsg_server", logrus.Fields{"raw": `a "b" c`}))},
		{"plain/empty-value", prodFormatter, goldenEntry(logrus.InfoLevel, "empty", moduleFields("dmsg_server", logrus.Fields{"empty": ""}))},
		{"plain/bool-and-float-values", prodFormatter, goldenEntry(logrus.InfoLevel, "typed", moduleFields("dmsg_server", logrus.Fields{"ok": true, "ratio": 1.5, "n": int64(-7)}))},
		{"plain/struct-value", prodFormatter, goldenEntry(logrus.InfoLevel, "typed", moduleFields("dmsg_server", logrus.Fields{"pt": struct {
			X int
			Y string
		}{1, "two"}}))},
		{"plain/nil-value", prodFormatter, goldenEntry(logrus.InfoLevel, "typed", moduleFields("dmsg_server", logrus.Fields{"nothing": nil}))},
		{"plain/module-with-colon", prodFormatter, goldenEntry(logrus.InfoLevel, "serving", moduleFields("dmsgC:disc:cxo-lookup", nil))},
		{"plain/empty-module", prodFormatter, goldenEntry(logrus.InfoLevel, "serving", moduleFields("", nil))},
		{"plain/skipped-prefix-key", prodFormatter, goldenEntry(logrus.InfoLevel, "serving", moduleFields("dmsg_server", logrus.Fields{"prefix": "ignored", "kept": "yes"}))},
		{"plain/many-fields", prodFormatter, goldenEntry(logrus.InfoLevel, "many", moduleFields("dmsg_server", logrus.Fields{
			"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "f": 6, "g": 7, "h": 8, "i": 9, "j": 10, "k": 11, "l": 12,
		}))},
		{"plain/exactly-eight-keys", prodFormatter, goldenEntry(logrus.InfoLevel, "eight", moduleFields("dmsg_server", logrus.Fields{
			"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "f": 6, "g": 7,
		}))},
		{"colored/many-fields", coloredFormatter, goldenEntry(logrus.InfoLevel, "many", moduleFields("dmsg_server", logrus.Fields{
			"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "f": 6, "g": 7, "h": 8, "i": 9, "j": 10, "k": 11, "l": 12,
		}))},
	}

	for _, lvl := range []logrus.Level{logrus.TraceLevel, logrus.DebugLevel, logrus.InfoLevel, logrus.WarnLevel, logrus.ErrorLevel, logrus.FatalLevel, logrus.PanicLevel} {
		lvl := lvl
		cases = append(cases,
			goldenCase{"level/plain/" + lvl.String(), prodFormatter, goldenEntry(lvl, "level check", moduleFields("dmsg_server", logrus.Fields{"k": "v"}))},
			goldenCase{"level/colored/" + lvl.String(), coloredFormatter, goldenEntry(lvl, "level check", moduleFields("dmsg_server", logrus.Fields{"k": "v"}))},
		)
	}

	cases = append(cases,
		goldenCase{"colored/no-fields", coloredFormatter, goldenEntry(logrus.InfoLevel, "serving", moduleFields("dmsg_server", nil))},
		goldenCase{"colored/no-module", coloredFormatter, goldenEntry(logrus.InfoLevel, "serving", nil)},
		goldenCase{"colored/three-fields", coloredFormatter, goldenEntry(logrus.InfoLevel, "stream closed", moduleFields("dmsg_server", logrus.Fields{"dst": "abc", "src": "def", "size": 4096}))},
		goldenCase{"colored/error-field", coloredFormatter, goldenEntry(logrus.ErrorLevel, "dial failed", moduleFields("dmsg_server", logrus.Fields{logrus.ErrorKey: errors.New("connection refused")}))},

		goldenCase{"critical/plain", prodFormatter, goldenEntry(logrus.ErrorLevel, "the sky is falling", logrus.Fields{logModuleKey: "dmsg_server", logPriorityKey: logPriorityCritical})},
		goldenCase{"critical/colored", coloredFormatter, goldenEntry(logrus.ErrorLevel, "the sky is falling", logrus.Fields{logModuleKey: "dmsg_server", logPriorityKey: logPriorityCritical})},
		goldenCase{"critical/colored-with-fields", coloredFormatter, goldenEntry(logrus.ErrorLevel, "the sky is falling", logrus.Fields{logModuleKey: "dmsg_server", logPriorityKey: logPriorityCritical, "k": "v"})},
		goldenCase{"critical/no-module", coloredFormatter, goldenEntry(logrus.ErrorLevel, "the sky is falling", logrus.Fields{logPriorityKey: logPriorityCritical})},
		goldenCase{"critical/non-critical-priority", coloredFormatter, goldenEntry(logrus.ErrorLevel, "odd priority", logrus.Fields{logModuleKey: "dmsg_server", logPriorityKey: "LOW"})},

		goldenCase{"callcontext/file-only", coloredFormatter, goldenEntry(logrus.InfoLevel, "cc", moduleFields("dmsg_server", logrus.Fields{"file": "server.go"}))},
		goldenCase{"callcontext/func-only", coloredFormatter, goldenEntry(logrus.InfoLevel, "cc", moduleFields("dmsg_server", logrus.Fields{"func": "Serve"}))},
		goldenCase{"callcontext/line-int", coloredFormatter, goldenEntry(logrus.InfoLevel, "cc", moduleFields("dmsg_server", logrus.Fields{"line": 42}))},
		goldenCase{"callcontext/line-string", coloredFormatter, goldenEntry(logrus.InfoLevel, "cc", moduleFields("dmsg_server", logrus.Fields{"line": "42"}))},
		goldenCase{"callcontext/line-uint64", coloredFormatter, goldenEntry(logrus.InfoLevel, "cc", moduleFields("dmsg_server", logrus.Fields{"line": uint64(42)}))},
		goldenCase{"callcontext/line-float-ignored", coloredFormatter, goldenEntry(logrus.InfoLevel, "cc", moduleFields("dmsg_server", logrus.Fields{"line": 42.5}))},
		goldenCase{"callcontext/all-three", coloredFormatter, goldenEntry(logrus.InfoLevel, "cc", moduleFields("dmsg_server", logrus.Fields{"file": "server.go", "func": "Serve", "line": 42}))},
		goldenCase{"callcontext/all-three-plain", prodFormatter, goldenEntry(logrus.InfoLevel, "cc", moduleFields("dmsg_server", logrus.Fields{"file": "server.go", "func": "Serve", "line": 42}))},
		goldenCase{"callcontext/empty-strings", coloredFormatter, goldenEntry(logrus.InfoLevel, "cc", moduleFields("dmsg_server", logrus.Fields{"file": "", "func": "", "line": ""}))},
		goldenCase{"callcontext/wrong-types", coloredFormatter, goldenEntry(logrus.InfoLevel, "cc", moduleFields("dmsg_server", logrus.Fields{"file": 7, "func": true}))},
		goldenCase{"callcontext/critical", coloredFormatter, goldenEntry(logrus.ErrorLevel, "cc", logrus.Fields{logModuleKey: "dmsg_server", logPriorityKey: logPriorityCritical, "file": "server.go", "line": 42})},

		goldenCase{"opt/no-timestamp", func() *TextFormatter {
			f := coloredFormatter()
			f.DisableTimestamp = true
			return f
		}, goldenEntry(logrus.InfoLevel, "no ts", moduleFields("dmsg_server", logrus.Fields{"k": "v"}))},
		goldenCase{"opt/no-timestamp-critical", func() *TextFormatter {
			f := coloredFormatter()
			f.DisableTimestamp = true
			return f
		}, goldenEntry(logrus.ErrorLevel, "no ts", logrus.Fields{logModuleKey: "dmsg_server", logPriorityKey: logPriorityCritical})},
		goldenCase{"opt/mini-timestamp", func() *TextFormatter {
			f := coloredFormatter()
			f.FullTimestamp = false
			return f
		}, goldenEntry(logrus.InfoLevel, "mini ts", moduleFields("dmsg_server", nil))},
		goldenCase{"opt/mini-timestamp-critical", func() *TextFormatter {
			f := coloredFormatter()
			f.FullTimestamp = false
			return f
		}, goldenEntry(logrus.InfoLevel, "mini ts", logrus.Fields{logModuleKey: "dmsg_server", logPriorityKey: logPriorityCritical})},
		goldenCase{"opt/default-timestamp-format", func() *TextFormatter {
			f := coloredFormatter()
			f.TimestampFormat = ""
			return f
		}, goldenEntry(logrus.InfoLevel, "rfc3339", moduleFields("dmsg_server", nil))},
		goldenCase{"opt/no-uppercase", func() *TextFormatter {
			f := coloredFormatter()
			f.DisableUppercase = true
			return f
		}, goldenEntry(logrus.WarnLevel, "lowercase level", moduleFields("dmsg_server", nil))},
		goldenCase{"opt/no-sorting", func() *TextFormatter {
			f := prodFormatter()
			f.DisableSorting = true
			return f
		}, goldenEntry(logrus.InfoLevel, "unsorted", moduleFields("dmsg_server", logrus.Fields{"only": "one"}))},
		goldenCase{"opt/space-padding", func() *TextFormatter {
			f := coloredFormatter()
			f.SpacePadding = 30
			return f
		}, goldenEntry(logrus.InfoLevel, "padded", moduleFields("dmsg_server", logrus.Fields{"k": "v"}))},
		goldenCase{"opt/space-padding-empty-message", func() *TextFormatter {
			f := coloredFormatter()
			f.SpacePadding = 10
			return f
		}, goldenEntry(logrus.InfoLevel, "", moduleFields("dmsg_server", nil))},
		goldenCase{"opt/space-padding-overflow", func() *TextFormatter {
			f := coloredFormatter()
			f.SpacePadding = 3
			return f
		}, goldenEntry(logrus.InfoLevel, "a much longer message", moduleFields("dmsg_server", nil))},
		goldenCase{"opt/space-padding-multibyte", func() *TextFormatter {
			f := coloredFormatter()
			f.SpacePadding = 12
			return f
		}, goldenEntry(logrus.InfoLevel, "héllo wörld", moduleFields("dmsg_server", nil))},
		goldenCase{"opt/space-padding-critical", func() *TextFormatter {
			f := coloredFormatter()
			f.SpacePadding = 30
			return f
		}, goldenEntry(logrus.ErrorLevel, "padded", logrus.Fields{logModuleKey: "dmsg_server", logPriorityKey: logPriorityCritical})},
		goldenCase{"opt/no-quoting", func() *TextFormatter {
			f := coloredFormatter()
			f.AlwaysQuoteStrings = false
			f.QuoteEmptyFields = false
			return f
		}, goldenEntry(logrus.InfoLevel, "quotes off", moduleFields("dmsg_server", logrus.Fields{"plain": "abc-1.2", "spaced": "a b", "empty": "", "err": errors.New("a b"), "errplain": errors.New("ab")}))},
		goldenCase{"opt/custom-quote-character", func() *TextFormatter {
			f := coloredFormatter()
			f.QuoteCharacter = "'"
			return f
		}, goldenEntry(logrus.InfoLevel, "custom quote", moduleFields("dmsg_server", logrus.Fields{"k": "v"}))},
		goldenCase{"opt/disable-colors-wins", func() *TextFormatter {
			f := coloredFormatter()
			f.DisableColors = true
			return f
		}, goldenEntry(logrus.InfoLevel, "no colors", moduleFields("dmsg_server", logrus.Fields{"k": "v"}))},
		goldenCase{"opt/custom-color-scheme", func() *TextFormatter {
			f := coloredFormatter()
			f.SetColorScheme(&ColorScheme{InfoLevelStyle: "magenta", PrefixStyle: "red+h"})
			return f
		}, goldenEntry(logrus.InfoLevel, "custom scheme", moduleFields("dmsg_server", logrus.Fields{"k": "v"}))},

		goldenCase{"unformatted/no-fields", func() *TextFormatter {
			f := prodFormatter()
			f.ForceFormatting = false
			return f
		}, goldenEntry(logrus.InfoLevel, "serving", moduleFields("dmsg_server", nil))},
		goldenCase{"unformatted/fields", func() *TextFormatter {
			f := prodFormatter()
			f.ForceFormatting = false
			return f
		}, goldenEntry(logrus.InfoLevel, "serving", moduleFields("dmsg_server", logrus.Fields{"dst": "abc", "n": 3}))},
		goldenCase{"unformatted/no-timestamp-no-message", func() *TextFormatter {
			f := prodFormatter()
			f.ForceFormatting = false
			f.DisableTimestamp = true
			return f
		}, goldenEntry(logrus.InfoLevel, "", moduleFields("dmsg_server", logrus.Fields{"dst": "abc"}))},
	)

	return cases
}

// render is the formatter under test, driven exactly as logrus drives it.
func render(t *testing.T, f *TextFormatter, entry *logrus.Entry) string {
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
		c := c
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
