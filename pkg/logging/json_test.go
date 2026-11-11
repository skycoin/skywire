package logging

import (
	"errors"
	"testing"
	"time"
)

// The expected line was produced by logrus' JSONFormatter for the same entry;
// the visor's JSON log readers depend on that layout.
func TestJSONFormatterMatchesLogrus(t *testing.T) {
	e := &Entry{
		Logger:  New(),
		Data:    Fields{"_module": "visor", "error": errors.New("boom <x>"), "n": 7, "msg": "clash", "s": "a&b", "f": 1.5, "nested": map[string]int{"a": 1}},
		Time:    time.Date(2026, 11, 4, 12, 30, 45, 123456789, time.UTC),
		Level:   WarnLevel,
		Message: `hello "q"`,
	}
	got, err := (&JSONFormatter{TimestampFormat: time.RFC3339Nano}).Format(e)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"_module":"visor","error":"boom \u003cx\u003e","f":1.5,"fields.msg":"clash","level":"warning","msg":"hello \"q\"","n":7,"nested":{"a":1},"s":"a\u0026b","time":"2026-11-04T12:30:45.123456789Z"}` + "\n"
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
