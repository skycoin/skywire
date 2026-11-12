// Package logging pkg/logging/level_gate_test.go c0-com-log
package logging

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type capture struct{ entries []*Entry }

func (c *capture) Levels() []Level { return AllLevels }
func (c *capture) Fire(e *Entry) error {
	c.entries = append(c.entries, e)
	return nil
}

// The gates follow the master logger's level, and Trace reaches a level the
// FieldLogger interface does not expose.
func TestLevelGates(t *testing.T) {
	old := GetLevel()
	defer SetLevel(old)

	SetLevel(InfoLevel)
	require.False(t, DebugEnabled())
	require.False(t, TraceEnabled())

	SetLevel(DebugLevel)
	require.True(t, DebugEnabled())
	require.False(t, TraceEnabled(), "debug must not turn on the per-stream trace")

	SetLevel(TraceLevel)
	require.True(t, DebugEnabled())
	require.True(t, TraceEnabled())
}

func TestTraceReachesTraceLevel(t *testing.T) {
	l := New()
	l.SetLevel(TraceLevel)
	l.SetOutput(discard{})
	c := &capture{}
	l.AddHook(c)

	Trace(l, "per-stream detail")
	require.Len(t, c.entries, 1)
	require.Equal(t, TraceLevel, c.entries[0].Level)
	require.Equal(t, "per-stream detail", c.entries[0].Message)

	// Below trace it emits nothing.
	l.SetLevel(DebugLevel)
	Trace(l, "dropped")
	require.Len(t, c.entries, 1)
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
