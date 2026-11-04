// Package logging pkg/logging/entry.go c0-com-log
package logging

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"reflect"
	"strings"
	"time"
)

// Entry is a log line in the making: the logger it writes through plus the
// fields attached so far. Hooks receive the finished entry, with Time, Level
// and Message set. An entry handed to a hook must not be retained.
type Entry struct {
	Logger  *MasterLogger
	Data    Fields
	Time    time.Time
	Level   Level
	Message string
	// Buffer is scratch space a Formatter may write into. It is set only
	// while the entry is being written.
	Buffer  *bytes.Buffer
	Context context.Context
}

func (e *Entry) dup(data Fields) *Entry {
	return &Entry{Logger: e.Logger, Data: data, Time: e.Time, Context: e.Context}
}

// WithField returns a copy of the entry with key set to value.
func (e *Entry) WithField(key string, value any) *Entry {
	d := e.dup(make(Fields, len(e.Data)+1))
	maps.Copy(d.Data, e.Data)
	d.addField(key, value)
	return d
}

// WithFields returns a copy of the entry with fields added.
func (e *Entry) WithFields(fields Fields) *Entry {
	d := e.dup(make(Fields, len(e.Data)+len(fields)))
	maps.Copy(d.Data, e.Data)
	for k, v := range fields {
		d.addField(k, v)
	}
	return d
}

// WithError returns a copy of the entry with err under ErrorKey.
func (e *Entry) WithError(err error) *Entry { return e.WithField(ErrorKey, err) }

// WithContext returns a copy of the entry carrying ctx.
func (e *Entry) WithContext(ctx context.Context) *Entry {
	d := e.dup(maps.Clone(e.Data))
	d.Context = ctx
	return d
}

// WithTime returns a copy of the entry stamped with t instead of the time it
// is logged.
func (e *Entry) WithTime(t time.Time) *Entry {
	d := e.dup(maps.Clone(e.Data))
	d.Time = t
	return d
}

// addField skips function values, which no formatter can render.
func (e *Entry) addField(key string, value any) {
	if _, ok := value.(error); !ok {
		t := reflect.TypeOf(value)
		if t != nil && (t.Kind() == reflect.Func || t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Func) {
			return
		}
	}
	if e.Data == nil {
		e.Data = make(Fields, 1)
	}
	e.Data[key] = value
}

// Bytes renders the entry with its logger's formatter.
func (e *Entry) Bytes() ([]byte, error) { return e.Logger.formatter().Format(e) }

// String renders the entry with its logger's formatter.
func (e *Entry) String() (string, error) {
	b, err := e.Bytes()
	return string(b), err
}

func (e *Entry) log(level Level, msg string, panicAfter bool) {
	n := e.dup(maps.Clone(e.Data))
	if n.Time.IsZero() {
		n.Time = time.Now()
	}
	n.Level = level
	n.Message = msg
	n.Logger.emit(n)
	if panicAfter {
		panic(n)
	}
}

// sprintln is fmt.Sprintln without the trailing newline, the line ending
// being the formatter's to add.
func sprintln(args ...any) string {
	if len(args) <= 1 {
		return fmt.Sprint(args...)
	}
	s := fmt.Sprintln(args...)
	return s[:len(s)-1]
}

// Log logs args at level.
func (e *Entry) Log(level Level, args ...any) {
	if e.Logger.IsLevelEnabled(level) {
		e.log(level, fmt.Sprint(args...), false)
	}
}

// Logf logs a formatted message at level.
func (e *Entry) Logf(level Level, format string, args ...any) {
	if e.Logger.IsLevelEnabled(level) {
		e.log(level, fmt.Sprintf(format, args...), false)
	}
}

// Logln logs args at level, spaced as fmt.Sprintln spaces them.
func (e *Entry) Logln(level Level, args ...any) {
	if e.Logger.IsLevelEnabled(level) {
		e.log(level, sprintln(args...), false)
	}
}

// Trace logs at TraceLevel.
func (e *Entry) Trace(args ...any) { e.Log(TraceLevel, args...) }

// Debug logs at DebugLevel.
func (e *Entry) Debug(args ...any) { e.Log(DebugLevel, args...) }

// Info logs at InfoLevel.
func (e *Entry) Info(args ...any) { e.Log(InfoLevel, args...) }

// Print logs at InfoLevel.
func (e *Entry) Print(args ...any) { e.Log(InfoLevel, args...) }

// Warn logs at WarnLevel.
func (e *Entry) Warn(args ...any) { e.Log(WarnLevel, args...) }

// Warning logs at WarnLevel.
func (e *Entry) Warning(args ...any) { e.Log(WarnLevel, args...) }

// Error logs at ErrorLevel.
func (e *Entry) Error(args ...any) { e.Log(ErrorLevel, args...) }

// Fatal logs at FatalLevel, then exits through the logger's ExitFunc.
func (e *Entry) Fatal(args ...any) {
	e.Log(FatalLevel, args...)
	e.Logger.Exit(1)
}

// Panic logs at PanicLevel, then panics with the entry.
func (e *Entry) Panic(args ...any) {
	if e.Logger.IsLevelEnabled(PanicLevel) {
		e.log(PanicLevel, fmt.Sprint(args...), true)
	}
}

// Tracef logs at TraceLevel.
func (e *Entry) Tracef(format string, args ...any) { e.Logf(TraceLevel, format, args...) }

// Debugf logs at DebugLevel.
func (e *Entry) Debugf(format string, args ...any) { e.Logf(DebugLevel, format, args...) }

// Infof logs at InfoLevel.
func (e *Entry) Infof(format string, args ...any) { e.Logf(InfoLevel, format, args...) }

// Printf logs at InfoLevel.
func (e *Entry) Printf(format string, args ...any) { e.Logf(InfoLevel, format, args...) }

// Warnf logs at WarnLevel.
func (e *Entry) Warnf(format string, args ...any) { e.Logf(WarnLevel, format, args...) }

// Warningf logs at WarnLevel.
func (e *Entry) Warningf(format string, args ...any) { e.Logf(WarnLevel, format, args...) }

// Errorf logs at ErrorLevel.
func (e *Entry) Errorf(format string, args ...any) { e.Logf(ErrorLevel, format, args...) }

// Fatalf logs at FatalLevel, then exits through the logger's ExitFunc.
func (e *Entry) Fatalf(format string, args ...any) {
	e.Logf(FatalLevel, format, args...)
	e.Logger.Exit(1)
}

// Panicf logs at PanicLevel, then panics with the entry.
func (e *Entry) Panicf(format string, args ...any) {
	if e.Logger.IsLevelEnabled(PanicLevel) {
		e.log(PanicLevel, fmt.Sprintf(format, args...), true)
	}
}

// Traceln logs at TraceLevel.
func (e *Entry) Traceln(args ...any) { e.Logln(TraceLevel, args...) }

// Debugln logs at DebugLevel.
func (e *Entry) Debugln(args ...any) { e.Logln(DebugLevel, args...) }

// Infoln logs at InfoLevel.
func (e *Entry) Infoln(args ...any) { e.Logln(InfoLevel, args...) }

// Println logs at InfoLevel.
func (e *Entry) Println(args ...any) { e.Logln(InfoLevel, args...) }

// Warnln logs at WarnLevel.
func (e *Entry) Warnln(args ...any) { e.Logln(WarnLevel, args...) }

// Warningln logs at WarnLevel.
func (e *Entry) Warningln(args ...any) { e.Logln(WarnLevel, args...) }

// Errorln logs at ErrorLevel.
func (e *Entry) Errorln(args ...any) { e.Logln(ErrorLevel, args...) }

// Fatalln logs at FatalLevel, then exits through the logger's ExitFunc.
func (e *Entry) Fatalln(args ...any) {
	e.Logln(FatalLevel, args...)
	e.Logger.Exit(1)
}

// Panicln logs at PanicLevel, then panics with the entry.
func (e *Entry) Panicln(args ...any) {
	if e.Logger.IsLevelEnabled(PanicLevel) {
		e.log(PanicLevel, sprintln(args...), true)
	}
}

// Writer returns a pipe whose lines are logged at InfoLevel. Close it when
// done.
func (e *Entry) Writer() *io.PipeWriter { return e.WriterLevel(InfoLevel) }

// WriterLevel returns a pipe whose lines are logged at level. Close it when
// done.
func (e *Entry) WriterLevel(level Level) *io.PipeWriter {
	r, w := io.Pipe()
	go func() {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, bufio.MaxScanTokenSize), bufio.MaxScanTokenSize)
		sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
			if len(data) >= bufio.MaxScanTokenSize {
				return bufio.MaxScanTokenSize, data[:bufio.MaxScanTokenSize], nil
			}
			return bufio.ScanLines(data, atEOF)
		})
		for sc.Scan() {
			msg := strings.TrimRight(sc.Text(), "\r\n")
			switch level {
			case FatalLevel:
				e.Fatal(msg)
			case PanicLevel:
				e.Panic(msg)
			default:
				e.Log(level, msg)
			}
		}
		if err := sc.Err(); err != nil {
			e.Errorf("Error while reading from Writer: %s", err)
		}
		r.Close() //nolint:errcheck,gosec
	}()
	return w
}

func stderrf(format string, args ...any) { fmt.Fprintf(os.Stderr, format, args...) } //nolint:errcheck
