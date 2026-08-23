// Package commands cmd/svc/route-finder/commands/root_test.go: unit tests for
// the testable surface of the route-finder root command — the JSON example
// helpers, buildConfig (flags + --config overlay), mergeFile, the command
// metadata/flags, and Execute's help path. The tiny RootCmd.Run closure boots a
// redis/dmsg-serving node and is not unit-testable.
//
// The standard "testing" package is imported as gotesting because the command
// declares a package-level `testing bool` flag var that would otherwise shadow
// the import.
package commands

import (
	"os"
	"path/filepath"
	gotesting "testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/services/rf"
)

// ---- exampleJSON / generateExamples ----------------------------------------

func TestExampleJSON(t *gotesting.T) {
	out := cmdutil.ExampleJSON(map[string]string{"version": "v1.3.29"})
	require.Contains(t, out, "v1.3.29")

	// Unmarshalable value (channel) → json.MarshalIndent fails → "".
	require.Equal(t, "", cmdutil.ExampleJSON(make(chan int)))
}

func TestGenerateExamples(t *gotesting.T) {
	out := generateExamples()
	require.NotEmpty(t, out)
	require.Contains(t, out, "Request/Response Examples:")
	require.Contains(t, out, "GET /health")
	require.Contains(t, out, "POST /routes")
	require.Contains(t, out, "e7a7f1b3c04047f89e12a0a1459b3456") // example tpID
}

// ---- buildConfig -----------------------------------------------------------

func TestBuildConfig_FromFlags(t *gotesting.T) {
	defer withGlobals(map[string]any{
		"addr": addr, "redisURL": redisURL, "tag": tag, "logLvl": logLvl,
		"configPath": configPath, "keyFile": keyFile,
	})()

	addr = ":1234"
	redisURL = "redis://example:6379"
	tag = "rf_test"
	logLvl = "debug"
	configPath = ""
	keyFile = ""

	cfg, err := buildConfig()
	require.NoError(t, err)
	require.Equal(t, ":1234", cfg.Addr)
	require.Equal(t, "redis://example:6379", cfg.Redis)
	require.Equal(t, "rf_test", cfg.Tag)
	require.Equal(t, "debug", cfg.LogLevel)
}

func TestBuildConfig_KeyfileGenerates(t *gotesting.T) {
	defer withGlobals(map[string]any{"keyFile": keyFile, "sk": sk, "configPath": configPath})()

	keyFile = filepath.Join(t.TempDir(), "rf.key")
	sk = cipher.SecKey{}
	configPath = ""

	cfg, err := buildConfig()
	require.NoError(t, err)
	require.NotEqual(t, cipher.SecKey{}, cfg.SecKey) // key was generated
	require.FileExists(t, keyFile)
}

func TestBuildConfig_ConfigFileOverrides(t *gotesting.T) {
	defer withGlobals(map[string]any{"addr": addr, "tag": tag, "configPath": configPath, "keyFile": keyFile})()

	addr = ":FLAG"
	tag = "flag_tag"
	keyFile = ""

	// Raw JSON (no key fields) so strict-parse doesn't reject a zero secret key.
	raw := []byte(`{"addr":":FILE","tag":"file_tag","mode":"dmsg","testing":true}`)
	path := filepath.Join(t.TempDir(), "rf.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	configPath = path

	cfg, err := buildConfig()
	require.NoError(t, err)
	require.Equal(t, ":FILE", cfg.Addr)   // file wins
	require.Equal(t, "file_tag", cfg.Tag) // file wins
	require.Equal(t, "dmsg", cfg.Mode)
	require.True(t, cfg.Testing)
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
	dst := &rf.Config{}
	pk, _ := cipher.GenerateKeyPair()
	src := &rf.Config{
		SecKey:          cipher.SecKey{1},
		Addr:            ":addr",
		MetricsAddr:     ":metrics",
		PprofAddr:       ":pprof",
		Redis:           "redis://x",
		RedisPoolSize:   5,
		LogLevel:        "debug",
		Tag:             "tag",
		Testing:         true,
		Mode:            "dual",
		SurveyWhitelist: []cipher.PubKey{pk},
		DmsgPort:        81,
		Dmsg: cmdutil.DmsgConfig{
			Discovery:  "http://disc",
			ServerType: "stcpr",
			Servers:    []*disc.Entry{{}},
		},
	}

	mergeFile(dst, src)

	// mergeFile copies the fields it knows about; assert each rather than the
	// whole struct (TestEnvironment is intentionally not merged).
	require.Equal(t, src.SecKey, dst.SecKey)
	require.Equal(t, src.Addr, dst.Addr)
	require.Equal(t, src.MetricsAddr, dst.MetricsAddr)
	require.Equal(t, src.PprofAddr, dst.PprofAddr)
	require.Equal(t, src.Redis, dst.Redis)
	require.Equal(t, src.RedisPoolSize, dst.RedisPoolSize)
	require.Equal(t, src.LogLevel, dst.LogLevel)
	require.Equal(t, src.Tag, dst.Tag)
	require.True(t, dst.Testing)
	require.Equal(t, src.Mode, dst.Mode)
	require.Equal(t, src.SurveyWhitelist, dst.SurveyWhitelist)
	require.Equal(t, src.DmsgPort, dst.DmsgPort)
	require.Equal(t, src.Dmsg, dst.Dmsg)
}

func TestMergeFile_ZeroSrcLeavesDst(t *gotesting.T) {
	orig := &rf.Config{
		Addr: ":keep", Tag: "keep", RedisPoolSize: 9, DmsgPort: 80,
		Dmsg: cmdutil.DmsgConfig{Discovery: "http://keep"},
	}
	dst := *orig

	mergeFile(&dst, &rf.Config{}) // empty src → no overrides
	require.Equal(t, *orig, dst)
}

// ---- command metadata / Execute --------------------------------------------

func TestRootCmd_Metadata(t *gotesting.T) {
	require.Equal(t, "Route Finder Server for skywire", RootCmd.Short)
	require.NotNil(t, RootCmd.Run)
	for _, name := range []string{"addr", "config", "redis", "testing", "mode", "dmsg-port", "keyfile"} {
		require.NotNil(t, RootCmd.Flags().Lookup(name), "flag %q should be registered", name)
	}
	require.Equal(t, ":9092", RootCmd.Flags().Lookup("addr").DefValue)
}

func TestExecute_Help(t *gotesting.T) {
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
			case "tag":
				tag = v.(string)
			case "logLvl":
				logLvl = v.(string)
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
