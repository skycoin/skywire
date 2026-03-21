package log

import (
	"bytes"
	"fmt"
	"testing"
)

func cleanLoggerOut(prefix string, debug bool) (l Logger, out *bytes.Buffer) {
	out = new(bytes.Buffer)
	c := NewConfig()
	c.Prefix = prefix
	c.Pins = All
	c.Debug = debug
	l = NewLogger(c)
	l.SetOutput(out)
	l.SetFlags(0)
	return
}

func TestNewLogger(t *testing.T) {
	var l Logger
	prefix := "prefix"
	var out *bytes.Buffer
	l, out = cleanLoggerOut(prefix, false)
	l.Print()
	if out.String() != fmt.Sprintln("prefix") { // add line break
		t.Errorf("wrong prefix: want %q, got %q", prefix, out.String())
	}
}

func TestLogger_Debug(t *testing.T) {
	t.Run("true", func(t *testing.T) {
		l, out := cleanLoggerOut("", true)
		l.Debug(All, "some")
		if some := "[DBG] " + fmt.Sprintln("some"); some != out.String() {
			t.Errorf("want %q, got %q", some, out.String())
		}
	})
	t.Run("false", func(t *testing.T) {
		l, out := cleanLoggerOut("", false)
		l.Debug(All, "some")
		if out.String() != "" {
			t.Error("got debug message even it disabled")
		}
	})
}

func TestLogger_Debugln(t *testing.T) {
	t.Run("true", func(t *testing.T) {
		l, out := cleanLoggerOut("", true)
		l.Debugln(All, "some")
		if some := "[DBG] " + fmt.Sprintln("some"); some != out.String() {
			t.Errorf("want %q, got %q", some, out.String())
		}
	})
	t.Run("false", func(t *testing.T) {
		l, out := cleanLoggerOut("", false)
		l.Debugln(All, "some")
		if out.String() != "" {
			t.Error("got debug message even it disabled")
		}
	})
}

func TestLogger_Debugf(t *testing.T) {
	t.Run("true", func(t *testing.T) {
		l, out := cleanLoggerOut("", true)
		l.Debugf(All, "some")
		if some := "[DBG] " + fmt.Sprintln("some"); some != out.String() {
			t.Errorf("want %q, got %q", some, out.String())
		}
	})
	t.Run("false", func(t *testing.T) {
		l, out := cleanLoggerOut("", false)
		l.Debugf(All, "some")
		if out.String() != "" {
			t.Error("got debug message even if it's disabled")
		}
	})
}

func TestLogger_Pins(t *testing.T) {

	buf := new(bytes.Buffer)

	c := NewConfig()
	c.Pins = 1
	c.Output = buf
	c.Debug = true

	l := NewLogger(c)

	if l.Pins() != 1 {
		t.Error("wrong pins")
	}

	l.SetFlags(0)

	l.Debug(1, "A")
	l.Debug(2, "B")
	l.Debug(3, "C")

	if buf.String() != "[DBG] A\n[DBG] C\n" {
		t.Error("pins work incorrect", buf.String())
	}

}
