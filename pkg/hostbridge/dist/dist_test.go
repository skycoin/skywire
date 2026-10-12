// Package dist pkg/hostbridge/dist/dist_test.go c3-vis-wasm
package dist

import (
	"runtime"
	"testing"
)

func TestPlatforms(t *testing.T) {
	p := Platforms()
	if runtime.GOOS != "js" {
		if len(p) != 0 {
			t.Fatalf("a native build embeds %d bridges, want none", len(p))
		}
		return
	}
	want := map[string]bool{
		"linux/amd64": true, "linux/arm64": true, "linux/arm": true, "linux/386": true,
		"darwin/amd64": true, "darwin/arm64": true, "windows/amd64": true,
	}
	for _, b := range p {
		delete(want, b.OS+"/"+b.Arch)
		exe, err := Open(b.File)
		if err != nil || int64(len(exe)) != b.Size || b.Size < 100<<10 {
			t.Fatalf("%s: %d bytes, err %v", b.File, len(exe), err)
		}
		magic := string(exe[:4])
		switch b.OS {
		case "linux":
			if magic != "\x7fELF" {
				t.Fatalf("%s is not ELF", b.File)
			}
		case "windows":
			if magic[:2] != "MZ" {
				t.Fatalf("%s is not PE", b.File)
			}
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing builds: %v", want)
	}
}

func TestOpenRefusesPaths(t *testing.T) {
	for _, f := range []string{"../dist.go", "a/b", `a\b`, "nope"} {
		if _, err := Open(f); err == nil {
			t.Fatalf("Open(%q) succeeded", f)
		}
	}
}
