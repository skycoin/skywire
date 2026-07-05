// Package commands cmd/svc/transport-discovery/commands/root_test.go: unit
// tests for the testable surface of the transport-discovery root command —
// the JSON example helpers, commaSplit, buildConfig (flags + --config overlay),
// mergeFile, the command metadata/flags, and Execute's help path.
//
// The standard "testing" package is imported as gotesting because the command
// declares a package-level `testing bool` flag var that would otherwise shadow
// the import.
package commands

import (
	"os"
	"path/filepath"
	gotesting "testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/services/tpd"
)

// ---- exampleJSON / generateExamples ----------------------------------------

func TestExampleJSON(t *gotesting.T) {
	out := exampleJSON(map[string]string{"version": "v1.3.29"})
	require.Contains(t, out, "v1.3.29")

	// Unmarshalable value (channel) → json.MarshalIndent fails → "".
	require.Equal(t, "", exampleJSON(make(chan int)))
}

func TestGenerateExamples(t *gotesting.T) {
	out := generateExamples()
	require.NotEmpty(t, out)
	require.Contains(t, out, "Request/Response Examples:")
	require.Contains(t, out, "GET /health")
	require.Contains(t, out, "GET /security/nonces/{pk}")
	require.Contains(t, out, "e7a7f1b3c04047f89e12a0a1459b3456") // example tpID
}

// ---- commaSplit ------------------------------------------------------------

func TestCommaSplit(t *gotesting.T) {
	require.Nil(t, commaSplit(""))
	require.Equal(t, []string{"a", "b", "c"}, commaSplit("a, b ,c"))
	require.Equal(t, []string{"x"}, commaSplit(" x "))
	require.Empty(t, commaSplit(" , , ")) // all blank after trim
}

// ---- buildConfig -----------------------------------------------------------

func TestBuildConfig_FromFlags(t *gotesting.T) {
	// Save & restore the package globals this test mutates.
	defer withGlobals(map[string]any{
		"addr": addr, "redisURL": redisURL, "tag": tag,
		"whitelistKeys": whitelistKeys, "configPath": configPath, "keyFile": keyFile,
	})()

	addr = ":1234"
	redisURL = "redis://example:6379"
	tag = "tpd_test"
	whitelistKeys = "pk1, pk2"
	configPath = ""
	keyFile = ""

	cfg, err := buildConfig()
	require.NoError(t, err)
	require.Equal(t, ":1234", cfg.Addr)
	require.Equal(t, "redis://example:6379", cfg.Redis)
	require.Equal(t, "tpd_test", cfg.Tag)
	require.Equal(t, []string{"pk1", "pk2"}, cfg.Whitelist)
}

func TestBuildConfig_KeyfileGenerates(t *gotesting.T) {
	defer withGlobals(map[string]any{"keyFile": keyFile, "sk": sk, "configPath": configPath})()

	keyFile = filepath.Join(t.TempDir(), "tpd.key")
	sk = cipher.SecKey{}
	configPath = ""

	cfg, err := buildConfig()
	require.NoError(t, err)
	require.NotEqual(t, cipher.SecKey{}, cfg.SecKey) // key was generated
	require.FileExists(t, keyFile)
}

func TestBuildConfig_ConfigFileOverrides(t *gotesting.T) {
	defer withGlobals(map[string]any{
		"addr": addr, "tag": tag, "configPath": configPath, "keyFile": keyFile,
	})()

	// Base flag values.
	addr = ":FLAG"
	tag = "flag_tag"
	keyFile = ""

	// File overrides addr + tag + mode, leaves others. Written as raw JSON
	// (omitting key fields) so strict-parse doesn't choke on a zero secret
	// key serialized by json.Marshal of a Config value.
	raw := []byte(`{"addr":":FILE","tag":"file_tag","mode":"dmsg"}`)
	path := filepath.Join(t.TempDir(), "tpd.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	configPath = path

	cfg, err := buildConfig()
	require.NoError(t, err)
	require.Equal(t, ":FILE", cfg.Addr)   // file wins
	require.Equal(t, "file_tag", cfg.Tag) // file wins
	require.Equal(t, "dmsg", cfg.Mode)
}

func TestBuildConfig_BadConfigPath(t *gotesting.T) {
	defer withGlobals(map[string]any{"configPath": configPath, "keyFile": keyFile})()

	keyFile = ""
	configPath = filepath.Join(t.TempDir(), "does-not-exist.json")

	_, err := buildConfig()
	require.Error(t, err)
}

// ---- mergeFile -------------------------------------------------------------

func TestMergeFile_AllFieldsOverride(t *gotesting.T) {
	dst := &tpd.Config{}
	pk, _ := cipher.GenerateKeyPair()
	src := &tpd.Config{
		SecKey:          cipher.SecKey{1},
		Addr:            ":addr",
		MetricsAddr:     ":metrics",
		PprofAddr:       ":pprof",
		Redis:           "redis://x",
		RedisPoolSize:   5,
		EntryTimeout:    services.Duration(time.Minute),
		LogLevel:        "debug",
		Tag:             "tag",
		Testing:         true,
		Mode:            "dual",
		TestEnvironment: true,
		Whitelist:       []string{"a"},
		SurveyWhitelist: []cipher.PubKey{pk},
		StoreDataPath:   "/data",
		UptimeDB:        "/uptime.db",
		DmsgPort:        81,
		Dmsg: cmdutil.DmsgConfig{
			Discovery:     "http://disc",
			DiscoveryDmsg: "dmsg://disc",
			ServerType:    "stcpr",
			Servers:       []*disc.Entry{{}},
		},
	}

	mergeFile(dst, src)
	require.Equal(t, src, dst) // every field copied over
}

func TestMergeFile_ZeroSrcLeavesDst(t *gotesting.T) {
	orig := &tpd.Config{
		Addr: ":keep", Tag: "keep", RedisPoolSize: 9, DmsgPort: 80,
		Dmsg: cmdutil.DmsgConfig{Discovery: "http://keep"},
	}
	dst := *orig // copy

	mergeFile(&dst, &tpd.Config{}) // empty src → no overrides
	require.Equal(t, *orig, dst)
}

// ---- command metadata / Execute --------------------------------------------

func TestRootCmd_Metadata(t *gotesting.T) {
	require.Equal(t, "Transport Discovery Server for skywire", RootCmd.Short)
	require.NotNil(t, RootCmd.Run)
	for _, name := range []string{"addr", "config", "redis", "testing", "mode", "dmsg-port", "keyfile"} {
		require.NotNil(t, RootCmd.Flags().Lookup(name), "flag %q should be registered", name)
	}
	require.Equal(t, ":9091", RootCmd.Flags().Lookup("addr").DefValue)
}

func TestExecute_Help(t *gotesting.T) {
	defer RootCmd.SetArgs(nil)
	RootCmd.SetArgs([]string{"--help"})
	RootCmd.SetOut(os.NewFile(0, os.DevNull))
	require.NotPanics(t, Execute)
}

// withGlobals snapshots the named package globals and returns a restore func.
// Only the globals listed are saved/restored, by name.
func withGlobals(saved map[string]any) func() {
	return func() {
		for name, v := range saved {
			switch name {
			case "addr":
				addr = v.(string)
			case "redisURL":
				redisURL = v.(string)
			case "tag":
				tag = v.(string)
			case "whitelistKeys":
				whitelistKeys = v.(string)
			case "configPath":
				configPath = v.(string)
			case "keyFile":
				keyFile = v.(string)
			case "sk":
				sk = v.(cipher.SecKey)
			}
		}
	}
}
