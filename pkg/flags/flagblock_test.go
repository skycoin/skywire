// Package flags pkg/flags/flagblock_test.go c0-com-util
package flags

import (
	"strings"
	"testing"

	"github.com/fatih/color"
	"github.com/spf13/pflag"
)

// forceColor turns fatih/color on for the duration of a test. It is off by
// default under `go test` because stdout is not a terminal, which would make
// every assertion about color vacuously pass.
func forceColor(t *testing.T) {
	t.Helper()
	prev := color.NoColor
	color.NoColor = false
	t.Cleanup(func() { color.NoColor = prev })
}

// TestEveryLineIsColored covers the shape coloredcobra misses most often: a
// wrapped description's continuation lines have no flag name on them, so the
// split-into-three-pieces rule finds two pieces and leaves the line bare.
func TestEveryLineIsColored(t *testing.T) {
	forceColor(t)

	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	fs.String("transport", "auto", "transport: auto (MultiDialer: skynet first, dmsg fallback) | dmsg | skynet (all via-visor) | tcp (direct-TCP, needs a host:port target)")

	block := FlagBlock(fs)
	lines := strings.Split(block, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected the description to wrap onto several lines, got:\n%s", block)
	}
	for i, l := range lines {
		if !strings.Contains(l, "\x1b[") {
			t.Errorf("line %d carries no color: %q", i, l)
		}
	}
}

// TestDescriptionWithDoubleSpaceIsColored covers the second shape: a
// description containing a run of two or more spaces splits into four pieces
// instead of three, and the whole line loses its description color.
func TestDescriptionWithDoubleSpaceIsColored(t *testing.T) {
	forceColor(t)

	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	fs.String("mode", "", "pick one:  fast  or  slow")

	block := FlagBlock(fs)
	if !strings.Contains(block, "\x1b[") {
		t.Fatalf("no color at all in:\n%q", block)
	}
	// The description must be inside a color run, not trailing after one.
	idx := strings.Index(block, "pick one")
	if idx < 0 {
		t.Fatalf("description missing from:\n%q", block)
	}
	if !strings.Contains(block[:idx], "\x1b[") {
		t.Errorf("description is not preceded by a color escape: %q", block)
	}
}

// TestFlagInDescriptionDoesNotEndTheColor covers the third shape, and the one
// that looks strangest on screen. coloredcobra colors up to two --flag tokens
// per line; when the description mentions a flag of its own, the second match
// lands inside the description, and the reset that ends it also ends the
// description's color — so the rest of the line goes bare from that word on.
func TestFlagInDescriptionDoesNotEndTheColor(t *testing.T) {
	forceColor(t)

	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	fs.String("jq", "", "filter JSON output through a jq expression (implies --json) and keep going")

	block := FlagBlock(fs)
	if !strings.Contains(block, "and keep going") {
		t.Fatalf("tail of the description missing from:\n%q", block)
	}
	// The bug is confined to a single line: a reset after the mention of
	// --json with visible text still to come on that same line leaves that
	// text bare. A reset at the end of a line is not the bug — every line
	// closes its own color and the next one opens its own.
	for _, l := range strings.Split(block, "\n") {
		m := strings.Index(l, "--json")
		if m < 0 {
			continue
		}
		rest := l[m+len("--json"):]
		i := strings.Index(rest, "\x1b[0m")
		if i < 0 {
			continue
		}
		if after := strings.TrimSpace(stripANSI(rest[i:])); after != "" {
			t.Errorf("color ends mid-line after --json, leaving %q bare", after)
		}
	}
}

// TestDefaultOnItsOwnLine pins the layout: the default is not appended to the
// description but placed on the line after it, in the description's column.
func TestDefaultOnItsOwnLine(t *testing.T) {
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	fs.String("port", "22", "port of remote visor dmsgpty")

	block := FlagBlock(fs)
	lines := strings.Split(block, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected a description line and a default line, got %d:\n%s", len(lines), block)
	}
	if strings.Contains(lines[0], "default") {
		t.Errorf("default is still on the description line: %q", lines[0])
	}
	if !strings.HasSuffix(lines[1], `(default "22")`) {
		t.Errorf("second line is not the default: %q", lines[1])
	}
	// Aligned with the description above it.
	descCol := strings.Index(lines[0], "port of")
	if got := len(lines[1]) - len(strings.TrimLeft(lines[1], " ")); got != descCol {
		t.Errorf("default indented to %d, description column is %d", got, descCol)
	}
}

// TestNothingExceedsHelpWidth is the width guarantee, over a flag set built to
// break it: a long description, a long default and a long flag name.
func TestNothingExceedsHelpWidth(t *testing.T) {
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	fs.String("transport", "auto", "transport: auto (MultiDialer: skynet first, dmsg fallback) | dmsg | skynet (all via-visor) | tcp (direct-TCP, needs a host:port target)")
	fs.String("rpc", "localhost:3435", "RPC server address")
	fs.Bool("standalone", false, "not supported for pty exec yet, accepted for vocabulary parity; errors if set")

	for _, l := range strings.Split(FlagBlock(fs), "\n") {
		if len(l) > HelpWidth {
			t.Errorf("line is %d wide, over the %d limit: %q", len(l), HelpWidth, l)
		}
	}
}

// keyValue stands in for cipher.SecKey: a custom pflag.Value whose zero is a
// string of zeros. pflag cannot tell that is a zero value, because it knows
// the type only by the name Type() returns.
type keyValue string

func (k *keyValue) String() string     { return string(*k) }
func (k *keyValue) Set(s string) error { *k = keyValue(s); return nil }
func (k *keyValue) Type() string       { return "cipher.SecKey" }

// TestAllZeroDefaultSuppressed keeps a 64-character key of zeros out of the
// help, where it says nothing and fits nowhere.
func TestAllZeroDefaultSuppressed(t *testing.T) {
	k := keyValue(strings.Repeat("0", 64))
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	fs.Var(&k, "sk", "local secret key")

	if block := FlagBlock(fs); strings.Contains(block, "default") {
		t.Errorf("all-zero default should be suppressed, got:\n%s", block)
	}
}

// A real default on the same custom type must still be shown.
func TestNonZeroCustomDefaultShown(t *testing.T) {
	k := keyValue("02" + strings.Repeat("0", 62))
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	fs.Var(&k, "pk", "remote public key")

	if block := FlagBlock(fs); !strings.Contains(block, "default") {
		t.Errorf("a real default should be shown, got:\n%s", block)
	}
}

// stripANSI removes color escapes, so an assertion can ask what the reader
// actually sees.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
