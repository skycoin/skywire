// Package appserver pkg/app/appserver/app_settings_test.go
package appserver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The store's contract in one test: a set bumps the version, a pull below that
// version hands the values over and records what the app says it applied, a
// pull at that version hands back nothing, and a stop clears the app.
func TestAppSettingsSetPullVersionClear(t *testing.T) {
	s := newAppSettings()

	vals, version := s.pull("skysocks-client", 0)
	require.Nil(t, vals)
	require.EqualValues(t, 0, version, "an app nobody has configured is at version 0")

	v1 := s.set("skysocks-client", map[string]int64{"chunk.max_bytes": 8 << 20})
	require.EqualValues(t, 1, v1)

	vals, version = s.pull("skysocks-client", 0)
	require.EqualValues(t, map[string]int64{"chunk.max_bytes": 8 << 20}, vals)
	require.EqualValues(t, 1, version)

	// The app now reports it applied version 1, so the next pull is empty and
	// the CLI sees applied == version.
	vals, version = s.pull("skysocks-client", 1)
	require.Nil(t, vals, "up to date: nothing to carry")
	require.EqualValues(t, 1, version)
	_, version, applied := s.state("skysocks-client")
	require.EqualValues(t, 1, version)
	require.EqualValues(t, 1, applied)

	// A reset is an empty set at a NEW version, so the app still learns of it.
	v2 := s.set("skysocks-client", nil)
	require.EqualValues(t, 2, v2)
	vals, version = s.pull("skysocks-client", 1)
	require.Nil(t, vals)
	require.EqualValues(t, 2, version)

	// Another app is untouched by all of it.
	_, version, _ = s.state("vpn-client")
	require.EqualValues(t, 0, version)

	s.clear("skysocks-client")
	_, version, applied = s.state("skysocks-client")
	require.EqualValues(t, 0, version, "a stopped app is back to the compiled defaults")
	require.EqualValues(t, 0, applied)
}

// The returned maps are copies: a caller mutating what it read must not reach
// into the store.
func TestAppSettingsCopiesOut(t *testing.T) {
	s := newAppSettings()
	in := map[string]int64{"pool.fill_interval": 1}
	s.set("app", in)
	in["pool.fill_interval"] = 2

	vals, _ := s.pull("app", 0)
	require.EqualValues(t, 1, vals["pool.fill_interval"])
	vals["pool.fill_interval"] = 3
	again, _, _ := s.state("app")
	require.EqualValues(t, 1, again["pool.fill_interval"])
}
