// Package commands cmd/svc/service-discovery/commands/root_test.go: unit tests
// for the testable surface of the service-discovery root command — the JSON
// example helpers, commaSplit, buildConfig (flags + --config overlay),
// mergeFile, the command metadata/flags, and Execute's help path. The tiny
// RootCmd.Run closure boots a redis/dmsg-serving node and is not unit-testable.
package commands

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/services/sd"
)

// ---- exampleJSON / generateExamples ----------------------------------------

func TestExampleJSON(t *testing.T) {
	out := exampleJSON(map[string]string{"version": "v1.3.29"})
	require.Contains(t, out, "v1.3.29")

	// Unmarshalable value (channel) → json.MarshalIndent fails → "".
	require.Equal(t, "", exampleJSON(make(chan int)))
}

func TestGenerateExamples(t *testing.T) {
	out := generateExamples()
	require.NotEmpty(t, out)
	require.Contains(t, out, "Request/Response Examples:")
	require.Contains(t, out, "GET /health")
	require.Contains(t, out, "GET /security/nonces/{pk}")
	require.Contains(t, out, "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5")
}

// ---- commaSplit ------------------------------------------------------------

func TestCommaSplit(t *testing.T) {
	require.Nil(t, commaSplit(""))
	require.Equal(t, []string{"a", "b", "c"}, commaSplit("a, b ,c"))
	require.Equal(t, []string{"x"}, commaSplit(" x "))
	require.Empty(t, commaSplit(" , , ")) // all blank after trim
}

// ---- buildConfig -----------------------------------------------------------

func TestBuildConfig_FromFlags(t *testing.T) {
	defer withGlobals(map[string]any{
		"addr": addr, "redisURL": redisURL, "geoipURL": geoipURL,
		"whitelistKeys": whitelistKeys, "configPath": configPath, "keyFile": keyFile,
	})()

	addr = ":1234"
	redisURL = "redis://example:6379"
	geoipURL = "http://geo.example"
	whitelistKeys = "pk1, pk2"
	configPath = ""
	keyFile = ""

	cfg, err := buildConfig()
	require.NoError(t, err)
	require.Equal(t, ":1234", cfg.Addr)
	require.Equal(t, "redis://example:6379", cfg.Redis)
	require.Equal(t, "http://geo.example", cfg.GeoIP)
	require.Equal(t, []string{"pk1", "pk2"}, cfg.Whitelist)
}

func TestBuildConfig_KeyfileGenerates(t *testing.T) {
	defer withGlobals(map[string]any{"keyFile": keyFile, "sk": sk, "configPath": configPath})()

	keyFile = filepath.Join(t.TempDir(), "sd.key")
	sk = cipher.SecKey{}
	configPath = ""

	cfg, err := buildConfig()
	require.NoError(t, err)
	require.NotEqual(t, cipher.SecKey{}, cfg.SecKey) // key was generated
	require.FileExists(t, keyFile)
}

func TestBuildConfig_ConfigFileOverrides(t *testing.T) {
	defer withGlobals(map[string]any{"addr": addr, "configPath": configPath, "keyFile": keyFile})()

	addr = ":FLAG"
	keyFile = ""

	// Raw JSON (no key fields) so strict-parse doesn't reject a zero secret key.
	raw := []byte(`{"addr":":FILE","mode":"dmsg","test_mode":true}`)
	path := filepath.Join(t.TempDir(), "sd.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	configPath = path

	cfg, err := buildConfig()
	require.NoError(t, err)
	require.Equal(t, ":FILE", cfg.Addr) // file wins
	require.Equal(t, "dmsg", cfg.Mode)
	require.True(t, cfg.TestMode)
}

func TestBuildConfig_BadConfigPath(t *testing.T) {
	defer withGlobals(map[string]any{"configPath": configPath, "keyFile": keyFile})()

	keyFile = ""
	configPath = filepath.Join(t.TempDir(), "does-not-exist.json")

	_, err := buildConfig()
	require.Error(t, err)
}

// ---- mergeFile -------------------------------------------------------------

func TestMergeFile_AllFieldsOverride(t *testing.T) {
	dst := &sd.Config{}
	pk, _ := cipher.GenerateKeyPair()
	src := &sd.Config{
		SecKey:          cipher.SecKey{1},
		Addr:            ":addr",
		MetricsAddr:     ":metrics",
		PprofAddr:       ":pprof",
		Redis:           "redis://x",
		EntryTimeout:    services.Duration(time.Minute),
		TestMode:        true,
		Mode:            "dual",
		Whitelist:       []string{"a"},
		SurveyWhitelist: []cipher.PubKey{pk},
		GeoIP:           "http://geo",
		DmsgPort:        81,
		Dmsg: cmdutil.DmsgConfig{
			Discovery:  "http://disc",
			ServerType: "stcpr",
			Servers:    []*disc.Entry{{}},
		},
	}

	mergeFile(dst, src)
	require.Equal(t, src, dst) // every field copied over
}

func TestMergeFile_ZeroSrcLeavesDst(t *testing.T) {
	orig := &sd.Config{
		Addr: ":keep", Redis: "redis://keep", GeoIP: "http://keep", DmsgPort: 80,
		Dmsg: cmdutil.DmsgConfig{Discovery: "http://keep"},
	}
	dst := *orig

	mergeFile(&dst, &sd.Config{}) // empty src → no overrides
	require.Equal(t, *orig, dst)
}

// ---- command metadata / Execute --------------------------------------------

func TestRootCmd_Metadata(t *testing.T) {
	require.Equal(t, "Service discovery server", RootCmd.Short)
	require.NotNil(t, RootCmd.Run)
	for _, name := range []string{"addr", "config", "redis", "test", "mode", "dmsg-port", "keyfile", "geoip"} {
		require.NotNil(t, RootCmd.Flags().Lookup(name), "flag %q should be registered", name)
	}
	require.Equal(t, ":9098", RootCmd.Flags().Lookup("addr").DefValue)
}

func TestExecute_Help(t *testing.T) {
	defer RootCmd.SetArgs(nil)
	RootCmd.SetArgs([]string{"--help"})
	RootCmd.SetOut(os.NewFile(0, os.DevNull))
	require.NotPanics(t, Execute)
}

// withGlobals snapshots the named package globals and returns a restore func.
func withGlobals(saved map[string]any) func() {
	return func() {
		for name, v := range saved {
			switch name {
			case "addr":
				addr = v.(string)
			case "redisURL":
				redisURL = v.(string)
			case "geoipURL":
				geoipURL = v.(string)
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
