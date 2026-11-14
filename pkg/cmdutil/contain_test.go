package cmdutil

import (
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/logging"
)

func TestContain(t *testing.T) {
	if err := Contain(func() {}); err != nil {
		t.Fatalf("clean run: %v", err)
	}
	if err := Contain(func() { Exit(3) }); err == nil || err.Error() != "exit status 3" {
		t.Fatalf("exit: got %v", err)
	}
	if err := Contain(func() { panic("boom") }); err == nil || !strings.HasPrefix(err.Error(), "panic: boom") {
		t.Fatalf("panic: got %v", err)
	}
	log := logging.MustGetLogger("contain-test")
	if err := Contain(func() { log.Fatal("fatal line") }); err == nil || err.Error() != "exit status 1" {
		t.Fatalf("fatal: got %v", err)
	}
}
