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

// TestDeskBootAttachBuildsTheWSPeer pins the attached boot: when the page is
// served with attach:{pk,path}, the visor the desk starts is told to hold ONE
// WebSocket transport back to the page origin (--ws-peer <pk>@<ws(s)://origin
// + path>) and to stay off public autoconnect. The address comes from
// location, so the same page works on any hostname the host is reached by.
func TestDeskBootAttachBuildsTheWSPeer(t *testing.T) {
	b, err := os.ReadFile("desk-boot.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"if (opts.attach && opts.attach.pk)",
		"location.host + (opts.attach.path || '/tp/ws')",
		"' --disable-public-autoconn --ws-peer ' + opts.attach.pk + '@' + tpURL",
		"initCmd: startVisor ? autoconfigCmd : ''",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("desk-boot.js lacks %q", want)
		}
	}
}
