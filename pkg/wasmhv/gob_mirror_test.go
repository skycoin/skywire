//go:build !js

// Package wasmhv gob_mirror_test.go: validates that the wasm-core MIRROR types
// (which deliberately do not import pkg/visor, so they compile to js/wasm)
// gob-decode faithfully from the REAL visor RPC types — and that the mirror
// argument types gob-encode into the real RPC argument types. This is the
// safety net for the "gob matches by field name" assumption the wasm
// hypervisor core relies on: a field rename on either side breaks a test here
// instead of silently returning zero values to the browser UI.
//
// Host-only (`!js`): it imports pkg/visor / appserver, which don't build for
// wasm — exactly why the mirror types exist.
package wasmhv

import (
	"bytes"
	"encoding/gob"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor"
)

func gobRoundTrip(t *testing.T, src, dst interface{}) {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(src))
	require.NoError(t, gob.NewDecoder(&buf).Decode(dst))
}

// TestMirrorAppStateDecodes confirms appserver.AppState → wasmhv.AppState.
func TestMirrorAppStateDecodes(t *testing.T) {
	real := &appserver.AppState{
		Status:         appserver.AppStatusRunning,
		DetailedStatus: "Running",
	}
	real.Name = "skysocks-client"
	real.Binary = "skysocks-client"
	real.Args = []string{"-srv", "abc"}
	real.AutoStart = true
	real.Port = 3
	real.LauncherMode = "external"

	var got AppState
	gobRoundTrip(t, real, &got)

	require.Equal(t, "skysocks-client", got.Name)
	require.Equal(t, "skysocks-client", got.Binary)
	require.Equal(t, []string{"-srv", "abc"}, got.Args)
	require.True(t, got.AutoStart)
	require.Equal(t, uint16(3), got.Port)
	require.Equal(t, "external", got.LauncherMode)
	require.Equal(t, int(appserver.AppStatusRunning), got.Status)
	require.Equal(t, "Running", got.DetailedStatus)
}

// TestMirrorTransportSummaryDecodes confirms visor.TransportSummary (including
// the custom-gob *transport.LogEntry) → wasmhv.TransportSummary.
func TestMirrorTransportSummaryDecodes(t *testing.T) {
	pkL, _ := cipher.GenerateKeyPair()
	pkR, _ := cipher.GenerateKeyPair()
	rb, sb := uint64(111), uint64(222)
	real := &visor.TransportSummary{
		ID:        uuid.New(),
		Local:     pkL,
		Remote:    pkR,
		Type:      "dmsg",
		Log:       &transport.LogEntry{RecvBytes: &rb, SentBytes: &sb},
		IsSetup:   false,
		Label:     "skycoin",
		LatencyMS: 42.5,
	}

	var got TransportSummary
	gobRoundTrip(t, real, &got)

	require.Equal(t, real.ID, got.ID)
	require.Equal(t, pkL, got.Local)
	require.Equal(t, pkR, got.Remote)
	require.Equal(t, "dmsg", got.Type)
	require.Equal(t, "skycoin", got.Label)
	require.Equal(t, 42.5, got.LatencyMS)
	require.NotNil(t, got.Log)
	require.Equal(t, uint64(111), got.Log.RecvBytes)
	require.Equal(t, uint64(222), got.Log.SentBytes)
}

// TestMirrorArgsEncode confirms the mirror RPC ARGUMENT types gob-encode into
// the real visor.* argument types (the direction a control call travels).
func TestMirrorArgsEncode(t *testing.T) {
	t.Run("StartAppIn", func(t *testing.T) {
		var got visor.StartAppIn
		gobRoundTrip(t, &startAppIn{AppName: "vpn-client", LauncherMode: "internal"}, &got)
		require.Equal(t, "vpn-client", got.AppName)
		require.Equal(t, "internal", got.LauncherMode)
	})
	t.Run("SetAutoStartIn", func(t *testing.T) {
		var got visor.SetAutoStartIn
		gobRoundTrip(t, &setAutoStartIn{AppName: "vpn-client", AutoStart: true}, &got)
		require.Equal(t, "vpn-client", got.AppName)
		require.True(t, got.AutoStart)
	})
	t.Run("TransportsIn", func(t *testing.T) {
		pk, _ := cipher.GenerateKeyPair()
		var got visor.TransportsIn
		gobRoundTrip(t, &transportsIn{FilterTypes: []string{"dmsg"}, FilterPubKeys: []cipher.PubKey{pk}, ShowLogs: true}, &got)
		require.Equal(t, []string{"dmsg"}, got.FilterTypes)
		require.Equal(t, []cipher.PubKey{pk}, got.FilterPubKeys)
		require.True(t, got.ShowLogs)
	})
	t.Run("AddTransportIn", func(t *testing.T) {
		pk, _ := cipher.GenerateKeyPair()
		var got visor.AddTransportIn
		gobRoundTrip(t, &addTransportIn{
			RemotePK: pk, TpType: "stcpr", Timeout: 30 * time.Second, Label: "user", NoRegister: true,
		}, &got)
		require.Equal(t, pk, got.RemotePK)
		require.Equal(t, "stcpr", got.TpType)
		require.Equal(t, 30*time.Second, got.Timeout)
		require.Equal(t, "user", got.Label)
		require.True(t, got.NoRegister)
	})
}
