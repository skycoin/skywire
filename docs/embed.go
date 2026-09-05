// Package docs docs/embed.go c1-cli-doc
// The prose half of `skywire doc serve`, embedded from where it lives.
//
// The `go:embed` directive cannot reach outside its own package directory, so
// the embed declaration sits in docs/ rather than beside the command that
// serves it.
// Nothing else belongs in this package.
//
// The CLI reference is NOT here and never will be: `skywire doc` walks the
// live cobra tree, so those pages ARE the binary and cannot drift from it.
// Only hand-written prose has to travel.
//
// The tree list below is explicit and must stay that way: a depth glob like
// `*/*.md` would sweep in the 528 generated pages under skywire/. TestProse-
// CoversEveryTree fails if a new prose directory is added without being listed
// here, which is how the list stopped being wrong the first time.
//
// Deliberately excluded:
//
//	graph/    37 MB of dependency graphs
//	img/      5.2 MB, mostly a 2.1 MB TUI gif and a CLI screenshot — both of
//	          surfaces the desk demonstrates live, so a still of them is the
//	          weaker copy
//	skywire/  the generated CLI reference, served from the cobra tree instead
//	specs/    derived by scripts/docs-prepare.sh at docs-build time and
//	rewards/  gitignored, so neither exists when `go build` runs
//	*.wasm    examples/routing-policies/wasm/ holds built policy modules. A
//	          whole-directory embed took 551 KB of them — 40% of the embed for
//	          nothing a reader can use — so examples/ is globbed at the depth
//	          its prose and .star sources actually live at instead.
//
// What remains is 102 files, ~960 KB, which gzips to roughly a quarter of
// that — about 2% of the wasm blob it rides in, and noise in a native build.
//
// Prose images are almost all remote (github.com/skycoin/.../assets/...): 26
// references across those files. With no transport they degrade to alt text;
// once a visor is running they load — a fairer demonstration than shipping
// them, and cheaper.
package docs

import "embed"

//go:embed *.md deploy deployment design guides packaging pty skychat skynet skysocks vpn
//go:embed examples/*/*.md examples/*/*.star
var proseFS embed.FS

// Prose returns the embedded prose tree: hand-written markdown, rooted at
// docs/. Paths are as they appear in the repo ("guides/foo.md").
func Prose() embed.FS { return proseFS }
