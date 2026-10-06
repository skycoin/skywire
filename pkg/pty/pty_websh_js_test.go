//go:build js && wasm

package pty

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/0magnet/afero"
)

// readUntil reads p's output until want appears or the time runs out.
func readUntil(t *testing.T, p *Pty, want string) string {
	t.Helper()
	var got bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for !strings.Contains(got.String(), want) {
			n, err := p.Read(buf)
			got.Write(buf[:n])
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("no %q in output: %q", want, got.String())
	}
	return got.String()
}

func TestWebshPtyRunsALine(t *testing.T) {
	webshFS = afero.NewMemMapFs
	p := NewPty()
	if err := p.Start("/bin/bash", nil, &WinSize{Rows: 24, Cols: 80}, nil); err != nil {
		t.Fatal(err)
	}
	readUntil(t, p, "$ ")
	if _, err := p.Write([]byte("echo hi there\r")); err != nil {
		t.Fatal(err)
	}
	out := readUntil(t, p, "hi there\r\n")
	if !strings.Contains(out, "echo hi there") {
		t.Fatalf("the typed line was not echoed: %q", out)
	}
	if _, err := p.Write([]byte("exit\r")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	for {
		if _, err := p.Read(buf); err == io.EOF {
			return
		} else if err != nil {
			t.Fatalf("read after exit: %v", err)
		}
	}
}
