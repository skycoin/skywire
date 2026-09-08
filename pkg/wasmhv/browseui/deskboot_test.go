package browseui

import (
	"os"
	"strings"
	"testing"
)

// TestDeskBootHasNoCodeBeforeItsHeader guards the shape of desk-boot.js: the
// file opens with its header comment and every statement lives inside the
// skywireDeskBoot definition. A block that lands OUTSIDE it runs at load time
// against names that only exist inside — which is exactly what shipped once
// (a terminal-window insert anchored on a line that occurs twice), throwing
// "opts is not defined" before skywireDeskBoot was ever defined and leaving
// the native hypervisor page blank.
func TestDeskBootHasNoCodeBeforeItsHeader(t *testing.T) {
	b, err := os.ReadFile("desk-boot.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if !strings.HasPrefix(src, "// pkg/wasmhv/browseui/desk-boot.js") {
		first, _, _ := strings.Cut(src, "\n")
		t.Fatalf("desk-boot.js must open with its header comment; first line is %q", first)
	}
	if !strings.Contains(src, "skywireDeskBoot") {
		t.Fatal("desk-boot.js does not define skywireDeskBoot")
	}
	// The boot options exist only inside the function that receives them, so
	// no code before its definition may touch them. Comments are allowed to
	// mention the name; code is not.
	def := strings.Index(src, "globalThis.skywireDeskBoot = function (opts)")
	if def < 0 {
		t.Fatal("cannot locate the skywireDeskBoot definition")
	}
	for i, line := range strings.Split(src[:def], "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "//") {
			continue
		}
		if strings.Contains(l, "opts.") {
			t.Fatalf("line %d references opts before skywireDeskBoot is defined: %q", i+1, l)
		}
	}
}
