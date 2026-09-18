// Package appserver pkg/app/appserver/app_settings_test.go
package appserver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The store's contract in one test: a set bumps the version, a pull below that
// version hands the values over and records what the app says it applied, a
// pull at that version hands back nothing, and a reset is an empty set at a
// NEW version rather than a hole in the counter.
func TestAppSettingsSetPullVersionReset(t *testing.T) {
	s := newAppSettings()

	vals, text, ops, version := s.pull("skysocks-client", 0, 0)
	require.Nil(t, vals)
	require.Nil(t, text)
	require.Nil(t, ops)
	require.EqualValues(t, 0, version, "an app nobody has configured is at version 0")

	v1 := s.set("skysocks-client", map[string]int64{"chunk.max_bytes": 8 << 20},
		map[string]string{"pool.require_tp_types": "stcpr"})
	require.EqualValues(t, 1, v1)

	vals, text, _, version = s.pull("skysocks-client", 0, 0)
	require.EqualValues(t, map[string]int64{"chunk.max_bytes": 8 << 20}, vals)
	require.EqualValues(t, map[string]string{"pool.require_tp_types": "stcpr"}, text)
	require.EqualValues(t, 1, version)

	// The app now reports it applied version 1, so the next pull is empty and
	// the CLI sees applied == version.
	vals, _, _, version = s.pull("skysocks-client", 1, 0)
	require.Nil(t, vals, "up to date: nothing to carry")
	require.EqualValues(t, 1, version)
	_, _, version, applied := s.state("skysocks-client")
	require.EqualValues(t, 1, version)
	require.EqualValues(t, 1, applied)

	// A reset is an empty set at a NEW version, so the app still learns of it.
	v2 := s.set("skysocks-client", nil, nil)
	require.EqualValues(t, 2, v2)
	vals, _, _, version = s.pull("skysocks-client", 1, 0)
	require.Nil(t, vals)
	require.EqualValues(t, 2, version)

	// Another app is untouched by all of it.
	_, _, version, _ = s.state("vpn-client")
	require.EqualValues(t, 0, version)

	vals, text, _, _ = s.state("skysocks-client")
	require.Empty(t, vals, "a reset holds nothing")
	require.Empty(t, text)
}

// The knobs an operator set SURVIVE the app process. Stop used to clear them,
// which made every restart a silent reset of a bench sweep.
func TestAppSettingsSurviveAppStop(t *testing.T) {
	m := &procManager{settings: newAppSettings(), procs: map[string]*Proc{}}

	m.SetAppSettings("skysocks-client", map[string]int64{"tunnel.count": 3},
		map[string]string{"pool.exclude_pks": "02" + "ab"})

	// Stop refuses (no such proc) — what matters is that nothing on the way
	// through it touched the knobs.
	require.Error(t, m.Stop("skysocks-client"))

	vals, text, version, _ := m.AppSettingsState("skysocks-client")
	require.EqualValues(t, 3, vals["tunnel.count"])
	require.Equal(t, "02ab", text["pool.exclude_pks"])
	require.EqualValues(t, 1, version)

	// A fresh process pulls them straight back at version 1.
	got, gotText, _, v := m.AppSettings("skysocks-client", 0, 0)
	require.EqualValues(t, 3, got["tunnel.count"])
	require.Equal(t, "02ab", gotText["pool.exclude_pks"])
	require.EqualValues(t, 1, v)

	// And the operator's reset is the one thing that forgets them.
	m.SetAppSettings("skysocks-client", nil, nil)
	vals, text, _, _ = m.AppSettingsState("skysocks-client")
	require.Empty(t, vals)
	require.Empty(t, text)
}

// An op is carried on every pull until the app acks its sequence, and then
// never again.
func TestAppSettingsOpsAckedBySequence(t *testing.T) {
	s := newAppSettings()

	seq := s.queueOp("skysocks-client", AppOpCutTunnel, 49170)
	require.EqualValues(t, 1, seq)

	_, _, ops, _ := s.pull("skysocks-client", 0, 0)
	require.Len(t, ops, 1)
	require.Equal(t, AppOpCutTunnel, ops[0].Kind)
	require.EqualValues(t, 49170, ops[0].Arg)

	// A pull that has not acked still carries it (an answer can be lost).
	_, _, ops, _ = s.pull("skysocks-client", 0, 0)
	require.Len(t, ops, 1)

	// Acked: gone, and a second op is sequenced after the first.
	_, _, ops, _ = s.pull("skysocks-client", 0, 1)
	require.Empty(t, ops)
	require.EqualValues(t, 2, s.queueOp("skysocks-client", AppOpCutTunnel, 49171))
	_, _, ops, _ = s.pull("skysocks-client", 0, 1)
	require.Len(t, ops, 1)
	require.EqualValues(t, 49171, ops[0].Arg)
}

// The returned maps are copies: a caller mutating what it read must not reach
// into the store.
func TestAppSettingsCopiesOut(t *testing.T) {
	s := newAppSettings()
	in := map[string]int64{"pool.fill_interval": 1}
	s.set("app", in, nil)
	in["pool.fill_interval"] = 2

	vals, _, _, _ := s.pull("app", 0, 0)
	require.EqualValues(t, 1, vals["pool.fill_interval"])
	vals["pool.fill_interval"] = 3
	again, _, _, _ := s.state("app")
	require.EqualValues(t, 1, again["pool.fill_interval"])
}
