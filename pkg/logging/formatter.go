// Package logging pkg/logging/formatter.go c0-com-log
package logging

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mgutz/ansi"
	"github.com/sirupsen/logrus"
)

const defaultTimestampFormat = time.RFC3339

// stackKeys is the number of entry fields whose key list fits in Format's
// stack scratch array. Skywire entries carry the module key plus a handful of
// fields, so the heap allocation is avoided on effectively every line.
const stackKeys = 8

var (
	baseTimestamp      = time.Now()
	defaultColorScheme = &ColorScheme{
		InfoLevelStyle:   "green",
		WarnLevelStyle:   "yellow",
		ErrorLevelStyle:  "red",
		FatalLevelStyle:  "red",
		PanicLevelStyle:  "red",
		DebugLevelStyle:  "blue",
		TraceLevelStyle:  "black",
		PrefixStyle:      "cyan",
		TimestampStyle:   "black+h",
		CallContextStyle: "black+h",
		CriticalStyle:    "magenta+h",
	}
	noColorsColorScheme = &compiledColorScheme{
		InfoLevelColor:   newANSIStyle(""),
		WarnLevelColor:   newANSIStyle(""),
		ErrorLevelColor:  newANSIStyle(""),
		FatalLevelColor:  newANSIStyle(""),
		PanicLevelColor:  newANSIStyle(""),
		DebugLevelColor:  newANSIStyle(""),
		TraceLevelColor:  newANSIStyle(""),
		PrefixColor:      newANSIStyle(""),
		TimestampColor:   newANSIStyle(""),
		CallContextColor: newANSIStyle(""),
		CriticalColor:    newANSIStyle(""),
	}
	defaultCompiledColorScheme = compileColorScheme(defaultColorScheme)
)

func miniTS() int {
	return int(time.Since(baseTimestamp) / time.Second)
}

// ColorScheme configures the logging output colors
type ColorScheme struct {
	InfoLevelStyle   string
	WarnLevelStyle   string
	ErrorLevelStyle  string
	FatalLevelStyle  string
	PanicLevelStyle  string
	DebugLevelStyle  string
	TraceLevelStyle  string
	PrefixStyle      string
	TimestampStyle   string
	CallContextStyle string
	CriticalStyle    string
}

// ansiStyle is a compiled color held as its raw escape prefix rather than as
// the closure ansi.ColorFunc returns, so colored text can be appended straight
// into the output buffer instead of allocating a new string for every element
// of every log line. Its semantics match ansi.ColorFunc exactly: an empty
// style is the identity, and any other style leaves an empty input untouched.
type ansiStyle struct {
	code     string
	identity bool
}

func newANSIStyle(style string) ansiStyle {
	if style == "" {
		return ansiStyle{identity: true}
	}
	return ansiStyle{code: ansi.ColorCode(style)}
}

// apply is the equivalent of ansi.ColorFunc(style)(s).
func (s ansiStyle) apply(str string) string {
	if s.identity || str == "" {
		return str
	}
	return s.code + str + ansi.Reset
}

// write appends str to b colored, without allocating.
func (s ansiStyle) write(b *bytes.Buffer, str string) {
	if s.identity || str == "" {
		b.WriteString(str) //nolint:gosec
		return
	}
	b.WriteString(s.code) //nolint:gosec
	b.WriteString(str)    //nolint:gosec
	b.WriteString(ansi.Reset)
}

// open and close bracket content appended to b piecewise. Callers must only
// use them around content that is never empty, since apply leaves an empty
// string uncolored.
func (s ansiStyle) open(b *bytes.Buffer) {
	if !s.identity {
		b.WriteString(s.code) //nolint:gosec
	}
}

func (s ansiStyle) close(b *bytes.Buffer) {
	if !s.identity {
		b.WriteString(ansi.Reset) //nolint:gosec
	}
}

type compiledColorScheme struct {
	InfoLevelColor   ansiStyle
	WarnLevelColor   ansiStyle
	ErrorLevelColor  ansiStyle
	FatalLevelColor  ansiStyle
	PanicLevelColor  ansiStyle
	DebugLevelColor  ansiStyle
	TraceLevelColor  ansiStyle
	PrefixColor      ansiStyle
	TimestampColor   ansiStyle
	CallContextColor ansiStyle
	CriticalColor    ansiStyle
}

// TextFormatter formats log output
type TextFormatter struct {
	// Set to true to bypass checking for a TTY before outputting colors.
	ForceColors bool

	// Force disabling colors. For a TTY colors are enabled by default.
	DisableColors bool

	// Force formatted layout, even for non-TTY output.
	ForceFormatting bool

	// Disable timestamp logging. useful when output is redirected to logging
	// system that already adds timestamps.
	DisableTimestamp bool

	// Disable the conversion of the log levels to uppercase
	DisableUppercase bool

	// Enable logging the full timestamp when a TTY is attached instead of just
	// the time passed since beginning of execution.
	FullTimestamp bool

	// Timestamp format to use for display when a full timestamp is printed.
	TimestampFormat string

	// The fields are sorted by default for a consistent output. For applications
	// that log extremely frequently and don't use the JSON formatter this may not
	// be desired.
	DisableSorting bool

	// Wrap empty fields in quotes if true.
	QuoteEmptyFields bool

	// Can be set to the override the default quoting character "
	// with something else. For example: ', or `.
	QuoteCharacter string

	// Pad msg field with spaces on the right for display.
	// The value for this parameter will be the size of padding.
	// Its default value is zero, which means no padding will be applied for msg.
	SpacePadding int

	// Always use quotes for string values (except for empty fields)
	AlwaysQuoteStrings bool

	// Color scheme to use.
	colorScheme *compiledColorScheme

	// Whether the logger's out is to a terminal.
	isTerminal bool

	sync.Once
}

func getCompiledColor(main string, fallback string) ansiStyle {
	var style string
	if main != "" {
		style = main
	} else {
		style = fallback
	}
	return newANSIStyle(style)
}

func compileColorScheme(s *ColorScheme) *compiledColorScheme {
	return &compiledColorScheme{
		InfoLevelColor:   getCompiledColor(s.InfoLevelStyle, defaultColorScheme.InfoLevelStyle),
		WarnLevelColor:   getCompiledColor(s.WarnLevelStyle, defaultColorScheme.WarnLevelStyle),
		ErrorLevelColor:  getCompiledColor(s.ErrorLevelStyle, defaultColorScheme.ErrorLevelStyle),
		FatalLevelColor:  getCompiledColor(s.FatalLevelStyle, defaultColorScheme.FatalLevelStyle),
		PanicLevelColor:  getCompiledColor(s.PanicLevelStyle, defaultColorScheme.PanicLevelStyle),
		DebugLevelColor:  getCompiledColor(s.DebugLevelStyle, defaultColorScheme.DebugLevelStyle),
		TraceLevelColor:  getCompiledColor(s.TraceLevelStyle, defaultColorScheme.TraceLevelStyle),
		PrefixColor:      getCompiledColor(s.PrefixStyle, defaultColorScheme.PrefixStyle),
		TimestampColor:   getCompiledColor(s.TimestampStyle, defaultColorScheme.TimestampStyle),
		CallContextColor: getCompiledColor(s.CallContextStyle, defaultColorScheme.CallContextStyle),
		CriticalColor:    getCompiledColor(s.CriticalStyle, defaultColorScheme.CriticalStyle),
	}
}

func (f *TextFormatter) init(entry *logrus.Entry) {
	if len(f.QuoteCharacter) == 0 {
		f.QuoteCharacter = "\""
	}
	if entry.Logger != nil {
		f.isTerminal = f.checkIfTerminal(entry.Logger.Out)
	}
}

// checkIfTerminal lives in formatter_istty_native.go / formatter_istty_js.go:
// isatty answers on native; under js/wasm stdout is always a pipe to the
// runtime and only the host knows a terminal renders it (it says so via TERM),
// so the visor's foreground logs in a browser terminal color like native.

// SetColorScheme sets the TextFormatter's color scheme configuration
func (f *TextFormatter) SetColorScheme(colorScheme *ColorScheme) {
	f.colorScheme = compileColorScheme(colorScheme)
}

// Format formats a logrus.Entry
func (f *TextFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	var b *bytes.Buffer

	var keyArr [stackKeys]string
	keys := keyArr[:0]
	if len(entry.Data) > stackKeys {
		keys = make([]string, 0, len(entry.Data))
	}
	for k := range entry.Data {
		keys = append(keys, k)
	}
	lastKeyIdx := len(keys) - 1

	// Nothing to order below two keys, and the overwhelming majority of
	// entries carry just the module key plus a field or two.
	if !f.DisableSorting && len(keys) > 1 {
		sort.Strings(keys)
	}
	if entry.Buffer != nil {
		b = entry.Buffer
	} else {
		b = &bytes.Buffer{}
	}

	f.Do(func() { f.init(entry) })

	isFormatted := f.ForceFormatting || f.isTerminal

	timestampFormat := f.TimestampFormat
	if timestampFormat == "" {
		timestampFormat = defaultTimestampFormat
	}
	if isFormatted {
		isColored := (f.ForceColors || f.isTerminal) && !f.DisableColors
		var colorScheme *compiledColorScheme
		if isColored {
			if f.colorScheme == nil {
				colorScheme = defaultCompiledColorScheme
			} else {
				colorScheme = f.colorScheme
			}
		} else {
			colorScheme = noColorsColorScheme
		}
		f.printColored(b, entry, keys, timestampFormat, colorScheme)
	} else {
		if !f.DisableTimestamp {
			f.appendKeyValue(b, "time", entry.Time.Format(timestampFormat), true)
		}
		f.appendKeyValue(b, "level", entry.Level.String(), true)
		if entry.Message != "" {
			f.appendKeyValue(b, "msg", entry.Message, lastKeyIdx >= 0)
		}
		for i, key := range keys {
			f.appendKeyValue(b, key, entry.Data[key], lastKeyIdx != i)
		}
	}

	b.WriteByte('\n') //nolint:gosec
	return b.Bytes(), nil
}

func (f *TextFormatter) printColored(b *bytes.Buffer, entry *logrus.Entry, keys []string, timestampFormat string, colorScheme *compiledColorScheme) {
	var levelColor ansiStyle
	switch entry.Level {
	case logrus.InfoLevel:
		levelColor = colorScheme.InfoLevelColor
	case logrus.WarnLevel:
		levelColor = colorScheme.WarnLevelColor
	case logrus.ErrorLevel:
		levelColor = colorScheme.ErrorLevelColor
	case logrus.FatalLevel:
		levelColor = colorScheme.FatalLevelColor
	case logrus.PanicLevel:
		levelColor = colorScheme.PanicLevelColor
	case logrus.TraceLevel:
		levelColor = colorScheme.TraceLevelColor
	default:
		levelColor = colorScheme.DebugLevelColor
	}

	module, priority := prefixParts(entry)
	hasPriority := priority == logPriorityCritical

	var levelText string
	if entry.Level != logrus.WarnLevel {
		levelText = entry.Level.String()
	} else {
		levelText = "warn"
	}

	if !f.DisableUppercase {
		levelText = upperLevelText(entry.Level, levelText)
	}

	message := entry.Message
	ccFile, ccFunc, ccLine := callContext(entry)
	hasCallContext := ccFile != "" || ccFunc != "" || ccLine != ""

	if hasPriority {
		// The critical color wraps the whole line, so it cannot be streamed
		// piecewise; this path is rare enough to leave on fmt.
		f.printCritical(b, entry, colorScheme, levelText, message, timestampFormat, ccFile, ccFunc, ccLine)
	} else {
		if !f.DisableTimestamp {
			colorScheme.TimestampColor.open(b)
			b.WriteByte('[') //nolint:gosec
			f.appendTimestamp(b, entry, timestampFormat)
			b.WriteByte(']') //nolint:gosec
			colorScheme.TimestampColor.close(b)
			b.WriteByte(' ') //nolint:gosec
		}
		levelColor.write(b, levelText)
		if hasCallContext {
			// The leading space sits outside the color, as it did when this
			// was " " + CallContextColor(strings.Join(parts, ":")).
			b.WriteByte(' ') //nolint:gosec
			colorScheme.CallContextColor.open(b)
			writeJoined(b, ccFile, ccFunc, ccLine)
			colorScheme.CallContextColor.close(b)
		}
		// extractPrefix never returns an empty string: with no module and no
		// priority it still renders "[]". The bracketed module is therefore
		// unconditional, and is written piecewise to skip the per-entry
		// Sprintf plus the " " + text + ":" concatenation it used to cost.
		colorScheme.PrefixColor.open(b)
		b.WriteString(" [") //nolint:gosec
		writePrefixBody(b, module, priority)
		b.WriteString("]:") //nolint:gosec
		colorScheme.PrefixColor.close(b)
		f.appendMessage(b, message)
	}

	for _, k := range keys {
		if k != "prefix" && k != "file" && k != "func" && k != "line" && k != logPriorityKey && k != logModuleKey {
			b.WriteByte(' ') //nolint:gosec
			levelColor.write(b, k)
			b.WriteByte('=') //nolint:gosec
			f.appendFormattedValue(b, entry.Data[k])
		}
	}
}

// printCritical renders a _priority=CRITICAL entry, whose whole line is wrapped
// in one color. Kept on fmt deliberately: it is a rare path and the padded
// message format is easier to get right there.
func (f *TextFormatter) printCritical(b *bytes.Buffer, entry *logrus.Entry, colorScheme *compiledColorScheme, levelText, message, timestampFormat string, ccFile, ccFunc, ccLine string) {
	messageFormat := "%s"
	if f.SpacePadding != 0 {
		messageFormat = fmt.Sprintf("%%-%ds", f.SpacePadding)
	}
	if message != "" {
		messageFormat = " " + messageFormat
	}

	var cc bytes.Buffer
	writeJoined(&cc, ccFile, ccFunc, ccLine)
	callContextText := cc.String()

	prefixText := extractPrefix(entry)
	if prefixText != "" {
		prefixText = " " + prefixText + ":"
	}

	var str string
	if f.DisableTimestamp {
		str = fmt.Sprintf("%s%s%s"+messageFormat, levelText, callContextText, prefixText, message)
	} else {
		var ts bytes.Buffer
		ts.WriteByte('[') //nolint:gosec
		f.appendTimestamp(&ts, entry, timestampFormat)
		ts.WriteByte(']') //nolint:gosec
		str = fmt.Sprintf("%s %s%s%s"+messageFormat, ts.String(), levelText, callContextText, prefixText, message)
	}
	b.WriteString(colorScheme.CriticalColor.apply(str)) //nolint:gosec
}

// upperLevelText returns the uppercased level name. strings.ToUpper allocates
// for the lowercase names logrus hands back, and the set is closed.
func upperLevelText(level logrus.Level, text string) string {
	switch level {
	case logrus.PanicLevel:
		return "PANIC"
	case logrus.FatalLevel:
		return "FATAL"
	case logrus.ErrorLevel:
		return "ERROR"
	case logrus.WarnLevel:
		return "WARN"
	case logrus.InfoLevel:
		return "INFO"
	case logrus.DebugLevel:
		return "DEBUG"
	case logrus.TraceLevel:
		return "TRACE"
	default:
		return strings.ToUpper(text)
	}
}

// writeJoined appends the non-empty parts separated by ":", as
// strings.Join(parts, ":") did over a slice built per entry.
func writeJoined(b *bytes.Buffer, parts ...string) {
	first := true
	for _, p := range parts {
		if p == "" {
			continue
		}
		if !first {
			b.WriteByte(':') //nolint:gosec
		}
		b.WriteString(p) //nolint:gosec
		first = false
	}
}

func writePrefixBody(b *bytes.Buffer, module, priority string) {
	switch {
	case priority == "":
		b.WriteString(module) //nolint:gosec
	case module == "":
		b.WriteString(priority) //nolint:gosec
	default:
		b.WriteString(module)   //nolint:gosec
		b.WriteByte(':')        //nolint:gosec
		b.WriteString(priority) //nolint:gosec
	}
}

// appendTimestamp writes the timestamp without its brackets.
func (f *TextFormatter) appendTimestamp(b *bytes.Buffer, entry *logrus.Entry, timestampFormat string) {
	if f.FullTimestamp {
		var scratch [64]byte
		b.Write(entry.Time.AppendFormat(scratch[:0], timestampFormat)) //nolint:gosec,errcheck
		return
	}

	// "%04d" over the seconds since process start.
	n := miniTS()
	if n < 0 {
		fmt.Fprintf(b, "%04d", n)
		return
	}
	var scratch [20]byte
	digits := strconv.AppendInt(scratch[:0], int64(n), 10)
	for i := len(digits); i < 4; i++ {
		b.WriteByte('0') //nolint:gosec
	}
	b.Write(digits) //nolint:gosec,errcheck
}

// appendMessage writes the message, padded to SpacePadding runes when set, as
// the "%s" / "%-<n>ds" format did.
func (f *TextFormatter) appendMessage(b *bytes.Buffer, message string) {
	if message != "" {
		b.WriteByte(' ') //nolint:gosec
	}
	if f.SpacePadding < 0 {
		// A negative width makes a malformed verb; leave that to fmt so the
		// output stays whatever it always was.
		fmt.Fprintf(b, fmt.Sprintf("%%-%ds", f.SpacePadding), message)
		return
	}
	b.WriteString(message) //nolint:gosec
	for n := f.SpacePadding - utf8.RuneCountInString(message); n > 0; n-- {
		b.WriteByte(' ') //nolint:gosec
	}
}

// callContext pulls the file/func/line keys out of the entry. Skywire sets
// none of them, so returning three strings keeps the common case free of the
// slice that used to be built and thrown away for every line.
func callContext(entry *logrus.Entry) (file, fn, line string) {
	if v, ok := entry.Data["file"]; ok {
		file, _ = v.(string)
	}
	if v, ok := entry.Data["func"]; ok {
		fn, _ = v.(string)
	}
	if v, ok := entry.Data["line"]; ok {
		switch v := v.(type) {
		case string:
			line = v
		case int:
			line = strconv.Itoa(v)
		case uint:
			line = strconv.FormatUint(uint64(v), 10)
		case int32:
			line = strconv.FormatInt(int64(v), 10)
		case int64:
			line = strconv.FormatInt(v, 10)
		case uint32:
			line = strconv.FormatUint(uint64(v), 10)
		case uint64:
			line = strconv.FormatUint(v, 10)
		}
	}
	return file, fn, line
}

func (f *TextFormatter) needsQuoting(text string) bool {
	if len(text) == 0 {
		return f.QuoteEmptyFields
	}

	if f.AlwaysQuoteStrings {
		return true
	}

	for _, ch := range text {
		if !((ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '.') {
			return true
		}
	}

	return false
}

func prefixParts(e *logrus.Entry) (module, priority string) {
	if iModule, ok := e.Data[logModuleKey]; ok {
		module, _ = iModule.(string)
	}
	if iPriority, ok := e.Data[logPriorityKey]; ok {
		priority, _ = iPriority.(string)
	}
	return module, priority
}

func extractPrefix(e *logrus.Entry) string {
	module, priority := prefixParts(e)

	switch {
	case priority == "":
		return "[" + module + "]"
	case module == "":
		return "[" + priority + "]"
	default:
		return "[" + module + ":" + priority + "]"
	}
}

// appendFormattedValue is the buffer-appending equivalent of the old
// formatKeyValue/formatValue pair, which cost two Sprintf calls per field.
func (f *TextFormatter) appendFormattedValue(b *bytes.Buffer, value interface{}) {
	switch value := value.(type) {
	case string:
		f.appendMaybeQuoted(b, value)
	case error:
		f.appendMaybeQuoted(b, value.Error())
	default:
		fmt.Fprintf(b, "%+v", value)
	}
}

func (f *TextFormatter) appendMaybeQuoted(b *bytes.Buffer, s string) {
	if !f.needsQuoting(s) {
		b.WriteString(s) //nolint:gosec
		return
	}
	b.WriteString(f.QuoteCharacter) //nolint:gosec
	b.WriteString(s)                //nolint:gosec
	b.WriteString(f.QuoteCharacter) //nolint:gosec
}

func (f *TextFormatter) appendKeyValue(b *bytes.Buffer, key string, value interface{}, appendSpace bool) {
	b.WriteString(key)      //nolint:gosec
	b.WriteByte('=')        //nolint:gosec
	f.appendValue(b, value) //nolint:gosec

	if appendSpace {
		b.WriteByte(' ') //nolint:gosec
	}
}

func (f *TextFormatter) appendValue(b *bytes.Buffer, value interface{}) {
	switch value := value.(type) {
	case string:
		if f.needsQuoting(value) {
			fmt.Fprintf(b, "%s%+v%s", f.QuoteCharacter, value, f.QuoteCharacter)
		} else {
			b.WriteString(value) //nolint:gosec
		}
	case error:
		errmsg := value.Error()
		if f.needsQuoting(errmsg) {
			fmt.Fprintf(b, "%s%+v%s", f.QuoteCharacter, errmsg, f.QuoteCharacter)
		} else {
			b.WriteString(errmsg) //nolint:gosec
		}
	default:
		fmt.Fprint(b, value)
	}
}
