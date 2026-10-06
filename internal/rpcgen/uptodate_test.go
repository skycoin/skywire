package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGeneratedUpToDate reruns every rpcgen go:generate directive in the module
// and fails if a checked in file differs from what rpcgen writes now.
func TestGeneratedUpToDate(t *testing.T) {
	root, _, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	const marker = "internal/rpcgen "
	n := 0
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "vendor", "node_modules", "testdata", ".git", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		data, err := os.ReadFile(p) //nolint:gosec
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, "//go:generate go run ") || !strings.Contains(line, marker) {
				continue
			}
			n++
			args := strings.Fields(line[strings.Index(line, marker)+len(marker):])
			cfg, err := parseArgs(args)
			if err != nil {
				t.Errorf("%s: %v", p, err)
				continue
			}
			cfg.dir = filepath.Join(filepath.Dir(p), cfg.dir)
			tiny, native, err := generate(cfg)
			if err != nil {
				t.Errorf("%s: %v", p, err)
				continue
			}
			for name, want := range map[string][]byte{cfg.out: tiny, cfg.native: native} {
				got, err := os.ReadFile(filepath.Join(cfg.dir, name)) //nolint:gosec
				if err != nil || !bytes.Equal(got, want) {
					t.Errorf("%s is stale, run go generate in %s", filepath.Join(cfg.dir, name), cfg.dir)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("found no rpcgen go:generate directives")
	}
	t.Logf("%d rpcgen directives checked", n)
}
