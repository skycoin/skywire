// Package treestore — pkg/cxo/treestore/path.go: path parsing.
//
// Paths are slash-separated strings. The empty string addresses the
// root. Segments must be non-empty and must not contain '/'. We
// deliberately don't support escaping — at the cost of disallowing
// slashes in segment names — so the parser stays trivial and
// path-printing round-trips cleanly.
package treestore

import (
	"errors"
	"strings"
)

// ErrInvalidPath is returned by SplitPath / JoinPath when a path
// contains an empty segment (e.g. leading/trailing slash, or "//").
var ErrInvalidPath = errors.New("treestore: invalid path (empty segment)")

// SplitPath splits a path into its segments. The empty path returns
// a nil slice. A single trailing slash is tolerated (so prefix-style
// callers like Walk("tiers/") work as users expect); any other empty
// segment (leading slash, "//", trailing "//") returns ErrInvalidPath.
func SplitPath(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		// The original was just "/" — also valid, equivalent to root.
		return nil, nil
	}
	segs := strings.Split(path, "/")
	for _, s := range segs {
		if s == "" {
			return nil, ErrInvalidPath
		}
	}
	return segs, nil
}

// JoinPath joins segments back into a path. Inverse of SplitPath.
// Returns ErrInvalidPath if any segment is empty or contains '/'.
func JoinPath(segs ...string) (string, error) {
	for _, s := range segs {
		if s == "" || strings.Contains(s, "/") {
			return "", ErrInvalidPath
		}
	}
	return strings.Join(segs, "/"), nil
}

// HasPrefix reports whether path is at-or-under the given prefix.
// Both inputs are validated for empty-segment correctness; an
// invalid prefix matches nothing. Prefix "" matches any path.
//
// "tiers/dmsg" is considered a prefix of "tiers/dmsg/2026-04-27" but
// NOT of "tiers/dmsg2" — segment-aware prefix, not byte-prefix.
//
// A trailing '/' on the prefix is normalized away: HasPrefix
// behaves identically for "foo/bar" and "foo/bar/". Without this
// every caller that wrote prefixes as "directories" (with trailing
// slash) tripped the segment-aware check below — the char after a
// trailing-slash prefix is a real path segment char, not '/',
// so the comparison returned false for paths that should clearly
// match. All five CXO feeds in the visor's CXOSubscriptionManager
// hit this and the subscriber-side snapshots were empty even when
// the publisher's Root contained the expected leaves.
func HasPrefix(path, prefix string) bool {
	if prefix == "" {
		return true
	}
	if prefix[len(prefix)-1] == '/' {
		prefix = prefix[:len(prefix)-1]
	}
	if prefix == "" {
		// Caller passed "/" — treat as "match anything".
		return true
	}
	if path == prefix {
		return true
	}
	// Segment-aware: require the next char after the prefix to be
	// the path separator so "tiers/d" doesn't match "tiers/dmsg".
	return len(path) > len(prefix) && path[len(prefix)] == '/' && path[:len(prefix)] == prefix
}
