#!/bin/sh
# Regenerate the dependency graph and the lines-of-code table in the README.
#
#   ./gendocs.sh          (or: make docs)
#
# Both sections are generated, so they drift the moment they are not
# regenerated — which is how one of them ended up pointing at an image that
# had never been committed. Run it when the dependencies or the code change.
#
# Needs goda, graphviz's dot, and gocloc:
#   go install github.com/loov/goda@latest
#   go install github.com/hhatto/gocloc/cmd/gocloc@latest
set -eu

cd "$(dirname "$0")"

mod=$(awk '/^module /{print $2; exit}' go.mod)
name=${mod##*/}
svg="docs/$name-goda-graph.svg"

for t in goda dot gocloc; do
	command -v "$t" >/dev/null 2>&1 || { echo "gendocs: $t not found on PATH" >&2; exit 1; }
done

# A wasm program's import edges live in js/wasm-tagged files, and a run in the
# host build context cannot see them — the graph comes out all but empty. So
# the build context follows the code.
if grep -rlq '^//go:build js' --include='*.go' . 2>/dev/null; then
	env GOOS=js GOARCH=wasm goda graph "$mod/..." | dot -Tsvg -o "$svg"
	cmd="GOOS=js GOARCH=wasm go run github.com/loov/goda@latest graph $mod/... | dot -Tsvg -o $svg"
	note='# GOOS=js: the import edges of a wasm program live in js/wasm-tagged
# files and are invisible to a host-context run'
else
	goda graph "$mod/..." | dot -Tsvg -o "$svg"
	cmd="go run github.com/loov/goda@latest graph $mod/... | dot -Tsvg -o $svg"
	note=''
fi

cloc=$(gocloc --not-match-d='(vendor|node_modules|\.git)' .)

# Rewritten rather than patched, so the two sections cannot drift apart.
awk '/^## Dependency Graph$/{exit} {print}' README.md > README.gendocs

{
	printf '## Dependency Graph\n\nMade with [goda](https://github.com/loov/goda):\n\n```\n'
	[ -n "$note" ] && printf '%s\n' "$note"
	printf '%s\n```\n\n![Dependency Graph](%s "%s Dependency Graph")\n\n' "$cmd" "$svg" "$mod"
	printf '## Lines of Code\n\nMade with [gocloc](https://github.com/hhatto/gocloc) (excludes `vendor/`, `node_modules/`, `.git/`):\n\n'
	printf '```\ngocloc --not-match-d='"'"'(vendor|node_modules|\\.git)'"'"' .\n```\n\n'
	printf '```\n%s\n```\n' "$cloc"
} >> README.gendocs

mv README.gendocs README.md
echo "gendocs: $svg and the code counts are up to date"
