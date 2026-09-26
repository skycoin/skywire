package docs

import (
	"io/fs"
	"os"
	"path"
	"strings"
	"testing"
)

// notProse are the trees deliberately left out of the embed. skywire/ is the
// GENERATED cli reference — `skywire doc serve` walks the cobra tree for that,
// so embedding 528 stale copies would be worse than useless. graph/ and img/
// are weight (37 MB and 5.2 MB). specs/ and rewards/ are staged by
// scripts/docs-prepare.sh at docs-build time and gitignored, so they do not
// exist when `go build` runs.
var notProse = map[string]bool{
	"skywire": true, "graph": true, "img": true,
	"specs": true, "rewards": true, "playground": true,
}

// TestProseCoversEveryTree fails when a prose directory exists on disk but is
// missing from the //go:embed line. The list has to be enumerated by hand — a
// depth glob would sweep in skywire/'s generated pages — and it was wrong on
// the first attempt: deploy/, skynet/ and skysocks/ were silently absent, so
// five files never shipped and nothing said so. This is the guard.
func TestProseCoversEveryTree(t *testing.T) {
	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read docs/: %v", err)
	}
	for _, e := range ents {
		if !e.IsDir() || notProse[e.Name()] || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		// A tree counts as prose once it holds any markdown.
		var hasMD bool
		if err := fs.WalkDir(os.DirFS(e.Name()), ".", func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
				hasMD = true
			}
			return nil
		}); err != nil {
			// A directory this test cannot walk is one it cannot vouch for,
			// which is the opposite of the assertion below passing.
			t.Fatalf("walk docs/%s/: %v", e.Name(), err)
		}
		if !hasMD {
			continue
		}
		if _, err := fs.Stat(proseFS, e.Name()); err != nil {
			t.Errorf("docs/%s/ holds prose but is not in the //go:embed line — add it", e.Name())
		}
	}
}

// TestProseExcludesGenerated is the other direction: the generated CLI
// reference must never creep into the embed. It is served from the live cobra
// tree, and a second copy would be stale the moment the tree changed.
func TestProseExcludesGenerated(t *testing.T) {
	for name := range notProse {
		if _, err := fs.Stat(proseFS, name); err == nil {
			t.Errorf("docs/%s/ is embedded but must not be", name)
		}
	}
}

// TestProseCarriesNoBuildOutput guards the byte budget. Embedding a directory
// takes EVERYTHING in it: the first version of this embed pulled 551 KB of
// built .wasm from examples/routing-policies/wasm — 40% of the embed, useless
// to a reader. Small companions the prose references (.star policy sources,
// compose.yaml, a Caddyfile) are wanted and stay.
func TestProseCarriesNoBuildOutput(t *testing.T) {
	banned := map[string]bool{".wasm": true, ".gz": true, ".png": true, ".gif": true, ".jpg": true, ".svg": true, ".zip": true}
	var total int64
	if err := fs.WalkDir(proseFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr == nil {
			total += info.Size()
		}
		if ext := path.Ext(p); banned[ext] {
			t.Errorf("%s embedded — build output does not belong in the docs embed", p)
		}
		return nil
	}); err != nil {
		// An unwalkable embed is an unmeasured embed; the size budget below
		// would otherwise pass on a total of zero.
		t.Fatalf("walk the prose embed: %v", err)
	}
	// The prose is ~762 KB today. Generous enough for ordinary writing, tight
	// enough that a tree of binaries trips it.
	if total > 2<<20 {
		t.Errorf("embedded prose is %.1f MB — over the 2 MB budget", float64(total)/(1<<20))
	}
}
