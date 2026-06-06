//go:build !js
// +build !js

package visorconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestUpdateBoolArg covers the bool-flag normalization: a true value must yield
// a bare double-dash "--flag" token (the form pflag-using in-tree apps expect),
// a false value must remove it, and prior malformed forms (the old single-dash
// "-flag=value" that made the launched app exit on startup) must be cleaned up.
func TestUpdateBoolArg(t *testing.T) {
	mk := func(args ...string) *Launcher {
		return &Launcher{Apps: appsList{{Name: "skysocks-client", Args: args}}}
	}
	got := func(l *Launcher) []string { return l.Apps[0].Args }

	t.Run("true adds a bare --flag", func(t *testing.T) {
		l := mk("--srv", "PK")
		updateBoolArg(l, "skysocks-client", "--reconnect", true)
		assert.Equal(t, []string{"--srv", "PK", "--reconnect"}, got(l))
	})

	t.Run("true is idempotent", func(t *testing.T) {
		l := mk("--srv", "PK", "--reconnect")
		updateBoolArg(l, "skysocks-client", "--reconnect", true)
		assert.Equal(t, []string{"--srv", "PK", "--reconnect"}, got(l))
	})

	t.Run("false removes the flag", func(t *testing.T) {
		l := mk("--srv", "PK", "--reconnect")
		updateBoolArg(l, "skysocks-client", "--reconnect", false)
		assert.Equal(t, []string{"--srv", "PK"}, got(l))
	})

	t.Run("cleans up the old single-dash -flag=value form", func(t *testing.T) {
		l := mk("--srv", "PK", "-reconnect=true")
		updateBoolArg(l, "skysocks-client", "--reconnect", true)
		assert.Equal(t, []string{"--srv", "PK", "--reconnect"}, got(l))
	})

	t.Run("cleans up a stray <flag> true pair", func(t *testing.T) {
		l := mk("--srv", "PK", "--reconnect", "true")
		updateBoolArg(l, "skysocks-client", "--reconnect", false)
		assert.Equal(t, []string{"--srv", "PK"}, got(l))
	})
}
