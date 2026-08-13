package cliout

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

type sample struct {
	Name  string `json:"name"`
	Count int    `json:"count,omitempty"`
}

func (s sample) Human(w io.Writer) error {
	_, err := io.WriteString(w, "name: "+s.Name+"\n")
	return err
}

// A nil result must still be a readable document. The old helper printed
// nothing, so a consumer piping into a parser got an empty file and a syntax
// error rather than "no result".
func TestNilEncodesAsNull(t *testing.T) {
	var b bytes.Buffer
	if err := Fprint(&b, true, nil); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "null\n" {
		t.Errorf("got %q, want \"null\\n\"", got)
	}
}

// Not a terminal, so compact — one line, which is what a parser wants.
func TestCompactWhenPiped(t *testing.T) {
	var b bytes.Buffer
	if err := Fprint(&b, true, sample{Name: "x", Count: 2}); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "{\"name\":\"x\",\"count\":2}\n" {
		t.Errorf("got %q", got)
	}
	if strings.Count(b.String(), "\n") != 1 {
		t.Errorf("expected a single line, got %q", b.String())
	}
}

// The value renders itself in text mode — the whole point, since the old
// signature took the human string separately and the two could disagree.
func TestHumanUsesTheValue(t *testing.T) {
	var b bytes.Buffer
	if err := Fprint(&b, false, sample{Name: "x", Count: 2}); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "name: x\n" {
		t.Errorf("got %q", got)
	}
}

// A value that cannot be marshaled must report it, not exit 0 with empty
// stdout as the old helper did.
func TestMarshalErrorIsReturned(t *testing.T) {
	var b bytes.Buffer
	err := Fprint(&b, true, make(chan int))
	if err == nil {
		t.Fatal("expected an error for an unmarshallable value")
	}
	var ute *json.UnsupportedTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("want a json.UnsupportedTypeError, got %T: %v", err, err)
	}
}
