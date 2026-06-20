// Package spec pkg/app/appserver/spec/spec_test.go
//
// AppConfig is a pure schema type with no executable statements, so there is no
// statement coverage to gain here. These tests instead guard the JSON wire
// format: AppConfig is embedded in visorconfig.V1's Launcher.Apps, so its field
// names and omitempty behaviour are a compatibility contract for on-disk visor
// configs. The tests fail loudly if a tag is renamed or an omitempty is dropped.
package spec

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
)

// TestAppConfigRoundTrip verifies a fully-populated AppConfig survives a
// marshal/unmarshal cycle unchanged.
func TestAppConfigRoundTrip(t *testing.T) {
	want := AppConfig{
		Name:          "skychat",
		Binary:        "skychat",
		Args:          []string{"-addr", ":8001"},
		AutoStart:     true,
		Port:          routing.Port(1),
		User:          "appuser",
		Group:         "appgroup",
		WorkDir:       "/var/lib/skywire/skychat",
		Env:           []string{"HOME=/home/appuser"},
		LauncherMode:  "external",
		RestartPolicy: "on-failure",
		RoutingPolicy: "@/etc/skywire/policy.star",
	}

	raw, err := json.Marshal(want)
	require.NoError(t, err)

	var got AppConfig
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, want, got)
}

// TestAppConfigWireFormat pins the JSON key names produced for a fully populated
// config.
func TestAppConfigWireFormat(t *testing.T) {
	raw, err := json.Marshal(AppConfig{
		Name:          "n",
		Binary:        "b",
		Args:          []string{"a"},
		AutoStart:     true,
		Port:          routing.Port(7),
		User:          "u",
		Group:         "g",
		WorkDir:       "w",
		Env:           []string{"K=V"},
		LauncherMode:  "internal",
		RestartPolicy: "always",
		RoutingPolicy: "p",
	})
	require.NoError(t, err)

	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &m))

	for _, key := range []string{
		"name", "binary", "args", "auto_start", "port", "user", "group",
		"work_dir", "env", "launcher_mode", "restart_policy", "routing_policy",
	} {
		_, ok := m[key]
		assert.Truef(t, ok, "expected wire key %q to be present", key)
	}
}

// TestAppConfigOmitEmpty verifies that only the non-omitempty fields are emitted
// for a zero-valued config, so minimal configs stay compact.
func TestAppConfigOmitEmpty(t *testing.T) {
	raw, err := json.Marshal(AppConfig{Name: "minimal"})
	require.NoError(t, err)

	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &m))

	// name, auto_start and port have no omitempty and must always appear.
	for _, key := range []string{"name", "auto_start", "port"} {
		_, ok := m[key]
		assert.Truef(t, ok, "non-omitempty key %q must always be present", key)
	}

	// Every optional field must be omitted when empty.
	for _, key := range []string{
		"binary", "args", "user", "group", "work_dir", "env",
		"launcher_mode", "restart_policy", "routing_policy",
	} {
		_, ok := m[key]
		assert.Falsef(t, ok, "omitempty key %q must be absent when empty", key)
	}
}
