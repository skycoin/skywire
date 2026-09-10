// Package logging pkg/logging/level_gate_test.go c0-com-util
package logging

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type capture struct{ entries []*logrus.Entry }

func (c *capture) Levels() []logrus.Level { return logrus.AllLevels }
func (c *capture) Fire(e *logrus.Entry) error {
	c.entries = append(c.entries, e)
	return nil
}

// The gates follow the master logger's level, and Trace reaches a level the
// logrus.FieldLogger interface does not expose.
func TestLevelGates(t *testing.T) {
	old := GetLevel()
	defer SetLevel(old)

	SetLevel(logrus.InfoLevel)
	require.False(t, DebugEnabled())
	require.False(t, TraceEnabled())

	SetLevel(logrus.DebugLevel)
	require.True(t, DebugEnabled())
	require.False(t, TraceEnabled(), "debug must not turn on the per-stream trace")

	SetLevel(logrus.TraceLevel)
	require.True(t, DebugEnabled())
	require.True(t, TraceEnabled())
}

func TestTraceReachesTraceLevel(t *testing.T) {
	l := logrus.New()
	l.SetLevel(logrus.TraceLevel)
	l.SetOutput(discard{})
	c := &capture{}
	l.AddHook(c)

	Trace(l, "per-stream detail")
	require.Len(t, c.entries, 1)
	require.Equal(t, logrus.TraceLevel, c.entries[0].Level)
	require.Equal(t, "per-stream detail", c.entries[0].Message)

	// Below trace it emits nothing.
	l.SetLevel(logrus.DebugLevel)
	Trace(l, "dropped")
	require.Len(t, c.entries, 1)
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
