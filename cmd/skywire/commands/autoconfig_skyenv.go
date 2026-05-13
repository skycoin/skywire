// Package commands cmd/skywire/commands/autoconfig_skyenv.go
//
// updateSkyenvFile applies a list of key/value edits to a bash-style
// env file (e.g. /etc/skywire.conf). For each edit:
//
//   - If a line `KEY=...` exists (commented `#KEY=...` or not), it is
//     replaced with the new `KEY=value` form, uncommented.
//   - If no such line exists, the edit is appended at the end of the
//     file under a "set by skywire autoconfig" header.
//
// Other lines (comments, unrelated settings, formatting) are preserved
// verbatim. Write is atomic: temp file + fsync + rename.
//
// This is the implementation behind the `skywire autoconfig --hvpks ...`
// family of flags that bypass having operators hand-edit the config
// file for the common knobs while still keeping that file as the
// single source of truth for `skywire cli config gen`.
package commands

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// skyenvEdit is one key/value update applied to a skyenv file.
// Value should already be formatted in the bash syntax expected for
// that key — single-quoted strings, bare booleans/ints, or
// '(...)'-wrapped arrays. See formatSkyenvBool / formatSkyenvString /
// formatSkyenvBashArray helpers below.
type skyenvEdit struct {
	Key   string // bash variable name (e.g. "HYPERVISORPKS")
	Value string // RHS, including quoting / array parens / value
}

// updateSkyenvFile applies the edits to the file at path. The file
// must already exist — `skywire autoconfig` always installs the
// template via the package, so this is a safe assumption for the
// operator-facing flow. Returns an error if the file is missing or
// the atomic rename fails.
//
// If two edits target the same key, the LATER one wins (mirrors the
// bash semantics of sequential assignment).
func updateSkyenvFile(path string, edits []skyenvEdit) error {
	if len(edits) == 0 {
		return nil
	}
	if path == "" {
		return fmt.Errorf("updateSkyenvFile: empty path")
	}

	// De-dupe + last-write-wins, preserving insertion order for the
	// append-tail case. Map provides constant-time lookup during the
	// scan; slice holds order for the eventual append.
	desired := make(map[string]string, len(edits))
	ordered := make([]string, 0, len(edits))
	for _, e := range edits {
		if _, ok := desired[e.Key]; !ok {
			ordered = append(ordered, e.Key)
		}
		desired[e.Key] = e.Value
	}

	src, err := os.Open(path) //nolint:gosec
	if err != nil {
		return fmt.Errorf("updateSkyenvFile: open %s: %w", path, err)
	}
	defer src.Close() //nolint:errcheck

	matched := make(map[string]bool, len(desired))
	out := make([]string, 0, 256)

	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20) // tolerate long lines
	for scanner.Scan() {
		line := scanner.Text()
		if k := skyenvLineKey(line); k != "" {
			if v, ok := desired[k]; ok {
				out = append(out, k+"="+v)
				matched[k] = true
				continue
			}
		}
		out = append(out, line)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("updateSkyenvFile: scan %s: %w", path, err)
	}

	// Append any keys we never matched. Tag the block so operators
	// scanning the file later can tell autoconfig wrote it.
	var appended []string
	for _, k := range ordered {
		if !matched[k] {
			appended = append(appended, k+"="+desired[k])
		}
	}
	if len(appended) > 0 {
		// Trim a trailing empty line on the existing file so the
		// header block stays visually attached without a double blank.
		for len(out) > 0 && out[len(out)-1] == "" {
			out = out[:len(out)-1]
		}
		out = append(out,
			"",
			"### Added by `skywire autoconfig` ######################################",
		)
		out = append(out, appended...)
	}

	// Atomic write: temp file in the same dir, fsync, rename. Same-dir
	// is required so the rename is on the same filesystem.
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".skywire.conf.tmp-")
	if err != nil {
		return fmt.Errorf("updateSkyenvFile: tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) } //nolint:errcheck
	w := bufio.NewWriter(tmp)
	for _, line := range out {
		if _, err := w.WriteString(line); err != nil {
			_ = tmp.Close() //nolint:errcheck,gosec
			cleanup()
			return fmt.Errorf("updateSkyenvFile: write: %w", err)
		}
		if _, err := w.WriteString("\n"); err != nil {
			_ = tmp.Close() //nolint:errcheck,gosec
			cleanup()
			return fmt.Errorf("updateSkyenvFile: write: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		_ = tmp.Close() //nolint:errcheck,gosec
		cleanup()
		return fmt.Errorf("updateSkyenvFile: flush: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close() //nolint:errcheck,gosec
		cleanup()
		return fmt.Errorf("updateSkyenvFile: sync: %w", err)
	}
	// Preserve original mode if possible; otherwise default to 0644
	// (autoconfig usually runs as root via systemd, and the file is
	// world-readable by design — operators view it without sudo).
	mode := os.FileMode(0644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close() //nolint:errcheck,gosec
		cleanup()
		return fmt.Errorf("updateSkyenvFile: chmod: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("updateSkyenvFile: close: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("updateSkyenvFile: rename: %w", err)
	}
	return nil
}

// skyenvKeyRE matches `KEY=` or `#KEY=` (with optional leading
// whitespace and optional space between `#` and the key). Captures the
// key name. Anchored at line start.
//
// Conservative on the LHS: only matches the bash-conventional shape
// (UPPER_CASE plus underscore + digits). Mixed-case `key=value` lines,
// rare in skywire.conf, are left untouched — autoconfig will append
// them as new lines instead of trying to replace.
var skyenvKeyRE = regexp.MustCompile(`^\s*#?\s*([A-Z_][A-Z0-9_]*)=`)

// skyenvLineKey returns the bash variable name from a line that looks
// like `KEY=value` or `#KEY=value`, or empty if the line isn't a
// recognizable variable assignment.
func skyenvLineKey(line string) string {
	m := skyenvKeyRE.FindStringSubmatch(line)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// formatSkyenvBool returns the bash-literal form for a boolean knob:
// "true" or "false". Bash treats both as bare words; the visor's
// scriptExecBool unwraps either to a Go bool.
func formatSkyenvBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// formatSkyenvString returns the bash-literal form for a string knob.
// Single-quoted with `'` escaped via `'\”`. Operators read these
// values verbatim; we don't try to be clever about it.
func formatSkyenvString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// formatSkyenvBashArray returns the `('a' 'b' 'c')` form expected by
// HYPERVISORPKS / DMSGPTYPKS / etc. Input is split on commas; each
// element is trimmed and single-quoted. Empty input produces `(”)`
// to match the template's default-empty representation.
func formatSkyenvBashArray(csv string) string {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, "'"+strings.ReplaceAll(p, "'", `'\''`)+"'")
	}
	if len(out) == 0 {
		return "('')"
	}
	return "(" + strings.Join(out, " ") + ")"
}

// formatSkyenvInt returns the bash-literal form for an int knob.
func formatSkyenvInt(i int) string { return fmt.Sprintf("%d", i) }
