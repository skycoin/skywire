package cmdutil

import (
	"os"
	"strings"
	"testing"
	"time"

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

// An Exit from a goroutine the run did not start ends the process even while
// a Contain is in progress, and one from a goroutine it did start ends the run.
func TestContainUnrelatedExit(t *testing.T) {
	exited := make(chan int, 1)
	osExit = func(code int) { exited <- code }
	t.Cleanup(func() { osExit = os.Exit })

	inside, release := make(chan struct{}), make(chan struct{})
	contained := make(chan error, 1)
	go func() {
		contained <- Contain(func() {
			close(inside)
			<-release
		})
	}()
	<-inside
	done := make(chan struct{})
	go func() {
		defer close(done)
		Exit(5)
	}()
	select {
	case code := <-exited:
		if code != 5 {
			t.Fatalf("os.Exit got %d, want 5", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the unrelated Exit did not reach os.Exit")
	}
	<-done
	close(release)
	if err := <-contained; err != nil {
		t.Fatalf("the Contain run was charged with the exit: %v", err)
	}

	err := Contain(func() {
		child := make(chan struct{})
		go func() {
			defer close(child)
			Exit(6)
		}()
		<-child
	})
	if err == nil || err.Error() != "exit status 6" {
		t.Fatalf("child exit: got %v", err)
	}
	select {
	case code := <-exited:
		t.Fatalf("a child's Exit reached os.Exit with %d", code)
	default:
	}
}
