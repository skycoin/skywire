// Package commands cmd/svc/address-resolver/commands/root_test.go: unit tests
// for the testable surface of the address-resolver root command — the JSON
// example helpers, commaSplit, buildConfig (flags + --config overlay),
// mergeFile, the command metadata/flags, and Execute's help path. The tiny
// RootCmd.Run closure boots a redis/dmsg-serving node and is not unit-testable.
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
	"github.com/skycoin/skywire/pkg/services/ar"
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
	require.Contains(t, out, "POST /bind/stcpr")
	require.Contains(t, out, "GET /security/nonces/{pk}")
}

// ---- commaSplit ------------------------------------------------------------

func TestCommaSplit(t *gotesting.T) {
	require.Nil(t, cmdutil.CommaSplit(""))
	require.Equal(t, []string{"a", "b", "c"}, cmdutil.CommaSplit("a, b ,c"))
	require.Equal(t, []string{"x"}, cmdutil.CommaSplit(" x "))
	require.Empty(t, cmdutil.CommaSplit(" , , ")) // all blank after trim
}

// ---- buildConfig -----------------------------------------------------------

func TestBuildConfig_FromFlags(t *gotesting.T) {
	defer withGlobals(map[string]any{
		"addr": addr, "udpAddr": udpAddr, "redisURL": redisURL, "tag": tag,
		"whitelistKeys": whitelistKeys, "configPath": configPath, "keyFile": keyFile,
	})()

	addr = ":1234"
	udpAddr = ":40000"
	redisURL = "redis://example:6379"
	tag = "ar_test"
	whitelistKeys = "pk1, pk2"
	configPath = ""
	keyFile = ""

	cfg, err := buildConfig()
	require.NoError(t, err)
	require.Equal(t, ":1234", cfg.Addr)
	require.Equal(t, ":40000", cfg.UDPAddr)
	require.Equal(t, "redis://example:6379", cfg.Redis)
	require.Equal(t, "ar_test", cfg.Tag)
	require.Equal(t, []string{"pk1", "pk2"}, cfg.Whitelist)
}

func TestBuildConfig_KeyfileGenerates(t *gotesting.T) {
	defer withGlobals(map[string]any{"keyFile": keyFile, "sk": sk, "configPath": configPath})()

	keyFile = filepath.Join(t.TempDir(), "ar.key")
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
	raw := []byte(`{"addr":":FILE","tag":"file_tag","mode":"dmsg","udp_addr":":50000"}`)
	path := filepath.Join(t.TempDir(), "ar.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	configPath = path

	cfg, err := buildConfig()
	require.NoError(t, err)
	require.Equal(t, ":FILE", cfg.Addr)   // file wins
	require.Equal(t, "file_tag", cfg.Tag) // file wins
	require.Equal(t, "dmsg", cfg.Mode)
	require.Equal(t, ":50000", cfg.UDPAddr)
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
	dst := &ar.Config{}
	pk, _ := cipher.GenerateKeyPair()
	src := &ar.Config{
		SecKey:          cipher.SecKey{1},
		Addr:            ":addr",
		UDPAddr:         ":udp",
		PublicUDPAddr:   ":pubudp",
		MetricsAddr:     ":metrics",
		PprofAddr:       ":pprof",
		Redis:           "redis://x",
		RedisPoolSize:   5,
		EntryTimeout:    services.Duration(time.Minute),
		Tag:             "tag",
		LogLevel:        "debug",
		Testing:         true,
		Mode:            "dual",
		Whitelist:       []string{"a"},
		SurveyWhitelist: []cipher.PubKey{pk},
		TestEnvironment: true,
		DmsgPort:        81,
		Dmsg: cmdutil.DmsgConfig{
			Discovery:  "http://disc",
			ServerType: "stcpr",
			Servers:    []*disc.Entry{{}},
		},
	}

	mergeFile(dst, src)
	require.Equal(t, src, dst) // mergeFile copies every Config field
}

func TestMergeFile_ZeroSrcLeavesDst(t *gotesting.T) {
	orig := &ar.Config{
		Addr: ":keep", UDPAddr: ":keepudp", Tag: "keep", RedisPoolSize: 9, DmsgPort: 80,
		Dmsg: cmdutil.DmsgConfig{Discovery: "http://keep"},
	}
	dst := *orig

	mergeFile(&dst, &ar.Config{}) // empty src → no overrides
	require.Equal(t, *orig, dst)
}

// ---- command metadata / Execute --------------------------------------------

func TestRootCmd_Metadata(t *gotesting.T) {
	require.Equal(t, "Address Resolver Server for skywire", RootCmd.Short)
	require.NotNil(t, RootCmd.Run)
	for _, name := range []string{"addr", "udp-addr", "config", "redis", "testing", "mode", "dmsg-port", "keyfile"} {
		require.NotNil(t, RootCmd.Flags().Lookup(name), "flag %q should be registered", name)
	}
	require.Equal(t, ":9093", RootCmd.Flags().Lookup("addr").DefValue)
	require.Equal(t, ":30178", RootCmd.Flags().Lookup("udp-addr").DefValue)
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
			case "udpAddr":
				udpAddr = v.(string)
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
