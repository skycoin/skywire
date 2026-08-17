#!/usr/bin/env bash
# scripts/graph-snapshot.sh — regenerate the static code-graph viewer under
# docs/graph/viewer/ from a running codebase-memory-mcp.
#
# The viewer that codebase-memory-mcp serves on localhost is a static three.js
# bundle plus one JSON endpoint: /api/layout returns every node with its x, y
# and z already assigned, because the layout is computed server-side. That means
# the whole thing works on a static host, with one wrinkle and one trick:
#
#   - the wrinkle: the bundle fetches /api/... and loads /assets/... by absolute
#     path, which resolves to the wrong place when the site is served from a
#     subdirectory. Both are rewritten to be relative here.
#   - the trick: a static host ignores the query string, so a single file at
#     api/layout answers /api/layout?project=skywire&max_nodes=5000 and every
#     other combination of parameters the viewer asks for. The node-count
#     control in the UI therefore does nothing on the published site; the
#     snapshot is whatever NODES was when it was generated.
#
# The upstream project is MIT licensed (Copyright (c) 2025 DeusData); the notice
# ships alongside the bundle in docs/graph/viewer/ATTRIBUTION.
#
#   ./scripts/graph-snapshot.sh                       # defaults below
#   NODES=20000 ./scripts/graph-snapshot.sh           # a denser snapshot
#   UI=http://localhost:9749 ./scripts/graph-snapshot.sh
#
# Prerequisites: codebase-memory-mcp running with its UI enabled, having indexed
# this repository. It derives a project name from wherever the checkout happens
# to live, so SRC_PROJECT is whatever `codebase-memory-mcp cli list_projects`
# reports; it is rewritten to PUB_PROJECT on the way out, and the result is
# checked, so no local path reaches the published site.

set -euo pipefail

cd "$(dirname "$0")/.."

UI="${UI:-http://localhost:9749}"
NODES="${NODES:-5000}"
PUB_PROJECT="${PUB_PROJECT:-skywire}"
OUT="${OUT:-docs/graph/viewer}"
BIN="${BIN:-codebase-memory-mcp}"
REPO_URL="${REPO_URL:-https://github.com/skycoin/skywire}"
REPO_BRANCH="${REPO_BRANCH:-develop}"

# Whichever local checkout was indexed: the name is derived from its path, so it
# is asked for rather than assumed. The first project whose name contains the
# repository name wins, which is right unless several checkouts are indexed —
# pass SRC_PROJECT to choose.
SRC_PROJECT="${SRC_PROJECT:-}"
if [ -z "$SRC_PROJECT" ]; then
	SRC_PROJECT=$("$BIN" cli list_projects 2>/dev/null |
		tr ',' '\n' |
		grep -o '"name":"[^"]*skywire[^"]*"' |
		head -1 |
		cut -d'"' -f4) || true
fi
if [ -z "$SRC_PROJECT" ]; then
	cat >&2 <<'MSG'
graph-snapshot: could not determine the indexed project name.

  codebase-memory-mcp cli list_projects
  SRC_PROJECT=<name> ./scripts/graph-snapshot.sh

If the binary is not on PATH, pass BIN=/path/to/codebase-memory-mcp.
MSG
	exit 1
fi

echo "graph-snapshot: $SRC_PROJECT -> $PUB_PROJECT, $NODES nodes, into $OUT"

rm -rf "$OUT"
mkdir -p "$OUT/assets" "$OUT/api"

# --- the shell page, with its asset paths made relative ----------------
curl -fsS "$UI/" -o "$OUT/index.html"
sed -i -E 's#(src|href)="/assets/#\1="assets/#g' "$OUT/index.html"

# Without ?project= the viewer asks the server which projects exist, over a POST
# to /rpc that a static host cannot answer — it then renders nothing at all. So
# the parameters are supplied if they are missing, which makes the page work
# however it is arrived at rather than only from a link that knows to add them.
sed -i "s#</head>#  <script>\n      // Added by scripts/graph-snapshot.sh — see ATTRIBUTION.\n      if (!new URLSearchParams(location.search).get('project')) {\n        location.replace('?tab=graph\&project=${PUB_PROJECT}' + location.hash);\n      }\n    </script>\n  </head>#" "$OUT/index.html"

# --- the bundle, with its asset and API paths made relative ------------
# The filenames are content-hashed, so they are read out of the page rather
# than assumed.
assets=$(grep -o '"assets/[A-Za-z0-9._-]*"' "$OUT/index.html" | tr -d '"')
if [ -z "$assets" ]; then
	echo "graph-snapshot: no assets found in the page — did its markup change?" >&2
	exit 1
fi
for asset in $assets; do
	curl -fsS "$UI/$asset" -o "$OUT/$asset"
	echo "  $asset"
done
for js in "$OUT"/assets/*.js; do
	[ -e "$js" ] || continue
	sed -i -E 's#"/api/#"api/#g; s#`/api/#`api/#g; s#"/assets/#"assets/#g' "$js"
done

# --- the data ---------------------------------------------------------
# One file per endpoint. The query string is ignored by a static host, so
# api/layout answers every request the viewer makes for a layout.
curl -fsS "$UI/api/layout?project=$SRC_PROJECT&max_nodes=$NODES" -o "$OUT/api/layout"

# The two innocuous ones the page reads on boot. Some endpoints want the project
# and some reject it, so both are tried; an empty object stands in if neither
# works, which the viewer tolerates.
for endpoint in ui-config adr; do
	curl -fsS "$UI/api/$endpoint" -o "$OUT/api/$endpoint" 2>/dev/null ||
		curl -fsS "$UI/api/$endpoint?project=$SRC_PROJECT" -o "$OUT/api/$endpoint" 2>/dev/null ||
		printf '{}' >"$OUT/api/$endpoint"
done

# index-status and processes are about the indexer's own state — process ids,
# resident memory, cpu time. None of it means anything once the page is a static
# file, so they are stubbed rather than captured.
printf '[]' >"$OUT/api/index-status"
printf '{"self_pid":0,"processes":[]}' >"$OUT/api/processes"

# repo-info is written rather than fetched. What the server reports is the local
# checkout — its absolute path and whatever branch happens to be out — and none
# of that belongs on a published page. Describing the repository the site is
# published from is both truthful and more useful: it is what the viewer builds
# its "open this file" links from.
cat >"$OUT/api/repo-info" <<EOF
{"root_path":"","branch":"$REPO_BRANCH","remote_url":"$REPO_URL","web_base":"$REPO_URL","blob_base":"$REPO_URL/blob/$REPO_BRANCH"}
EOF

# The indexed name is derived from the checkout path and prefixes every
# qualified name in the payload. Rewrite it so the published site says nothing
# about anybody's home directory.
sed -i "s#${SRC_PROJECT}#${PUB_PROJECT}#g" "$OUT/api/layout"

# Then prove it. The needles are the indexed name and the checkout path — not a
# bare "/home/", because the graph legitimately contains this repository's HTTP
# routes and one of them is /home/u.
for needle in "$SRC_PROJECT" "$PWD" "$HOME"; do
	[ -n "$needle" ] || continue
	if grep -qF -- "$needle" "$OUT"/api/* "$OUT"/index.html "$OUT"/assets/*; then
		echo "graph-snapshot: '$needle' is still in the output" >&2
		exit 1
	fi
done

# And check it is still JSON, since the rewrite above is textual.
if command -v jq >/dev/null 2>&1; then
	jq -e '.nodes and .edges' "$OUT/api/layout" >/dev/null ||
		{ echo "graph-snapshot: the layout is not valid JSON after rewriting" >&2; exit 1; }
	echo "graph-snapshot: $(jq -r '"\(.nodes|length) nodes, \(.edges|length) edges, of \(.total_nodes) indexed"' "$OUT/api/layout")"
else
	echo "graph-snapshot: jq not found; skipped the JSON check"
fi

cat >"$OUT/ATTRIBUTION" <<'EOF'
The viewer in this directory (index.html and assets/) is the graph UI from
codebase-memory-mcp, redistributed with two changes: its asset and API paths are
made relative so it can be served from a subdirectory, and index.html supplies
default query parameters when it is opened without any, because without them the
viewer asks the server to enumerate projects over a POST that a static host
cannot answer.

    https://github.com/DeusData/codebase-memory-mcp

MIT License. Copyright (c) 2025 DeusData.

api/layout is generated from this repository by scripts/graph-snapshot.sh.
EOF

echo "graph-snapshot: done"
du -sh "$OUT"
