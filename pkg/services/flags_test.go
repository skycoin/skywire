// Package services pkg/services/flags_test.go
package services

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestMergeCommonCoversEveryField sets every Common field and requires the
// merge to carry all of them, so a field added later cannot be silently
// dropped from a service's config file the way dmsg-discovery's pprof and
// log settings were.
func TestMergeCommonCoversEveryField(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	var src Common
	v := reflect.ValueOf(&src).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		switch f.Interface().(type) {
		case cipher.PubKey:
			f.Set(reflect.ValueOf(pk))
		case cipher.SecKey:
			f.Set(reflect.ValueOf(sk))
		case Duration:
			f.Set(reflect.ValueOf(Duration(time.Minute)))
		case []cipher.PubKey:
			f.Set(reflect.ValueOf([]cipher.PubKey{pk}))
		default:
			switch f.Kind() {
			case reflect.String:
				f.SetString("x")
			case reflect.Bool:
				f.SetBool(true)
			case reflect.Int:
				f.SetInt(7)
			case reflect.Uint16:
				f.SetUint(7)
			default:
				t.Fatalf("field %s: teach this test its type %s", v.Type().Field(i).Name, f.Type())
			}
		}
	}

	var dst Common
	MergeCommon(&dst, src)
	require.Equal(t, src, dst)

	// A zero source changes nothing.
	kept := dst
	MergeCommon(&dst, Common{})
	require.Equal(t, kept, dst)
}

// TestFlagsBindSameSetEverywhere: every service gets the same shared flags
// and shorthands, whatever its defaults.
func TestFlagsBindSameSetEverywhere(t *testing.T) {
	names := func(d FlagDefaults) []string {
		fs := pflag.NewFlagSet("x", pflag.ContinueOnError)
		var f Flags
		f.Bind(fs, d)
		var out []string
		fs.VisitAll(func(fl *pflag.Flag) { out = append(out, fl.Name+"/"+fl.Shorthand) })
		sort.Strings(out)
		return out
	}
	a := names(FlagDefaults{Gen: "tpd", Addr: ":9091", Tag: "transport_discovery", EntryTimeout: time.Minute})
	b := names(FlagDefaults{Gen: "dmsgdisc", Addr: ":9090", Tag: "dmsg_disc", EntryTimeout: time.Hour})
	require.Equal(t, a, b)
	require.Contains(t, a, "pprofmode/q")
	require.Contains(t, a, "pprofaddr/r")
	require.Contains(t, a, "loglvl/l")
}

// TestFlagsLegacySpellings: the old per-service spellings still parse.
func TestFlagsLegacySpellings(t *testing.T) {
	fs := pflag.NewFlagSet("x", pflag.ContinueOnError)
	var f Flags
	f.Bind(fs, FlagDefaults{Gen: "tpd", Addr: ":9091", Tag: "t"})
	require.NoError(t, fs.Parse([]string{"--pprof", ":6094", "--dmsgPort", "81", "--syslog-lvl", "debug"}))
	require.Equal(t, ":6094", f.PprofAddr)
	require.EqualValues(t, 81, f.DmsgPort)
	require.Equal(t, "debug", f.LogLevel)
}
