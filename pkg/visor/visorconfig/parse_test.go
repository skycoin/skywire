package visorconfig

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

func TestParse(t *testing.T) {
	jsonString := func(v interface{}) string {
		j, err := json.Marshal(v)
		require.NoError(t, err)
		return string(j)
	}

	// Given a config file with only the 'version' and 'sk' fields defined,
	// 'visorconfig.Parse' SHOULD fill the file with base values - as defined in 'visorconf.BaseConfig'.
	t.Run("parseV1_fill_base", func(t *testing.T) {
		// init
		f, err := ioutil.TempFile(os.TempDir(), "*.json")
		require.NoError(t, err)

		filename := f.Name()
		defer func() { require.NoError(t, os.Remove(filename)) }()

		_, sk := cipher.GenerateKeyPair()
		version := Version()
		raw := []byte(fmt.Sprintf(`{"version":"%s","sk":"%s"}`, version, sk.String()))
		n, err := f.Write(raw)
		require.NoError(t, err)
		require.Len(t, raw, n)
		require.NoError(t, f.Close())

		// check: obtained config contains all base values.
		services := &Services{}
		conf, err := Parse(nil, filename, raw, false, false, services)
		require.NoError(t, err)
		require.JSONEq(t, jsonString(MakeBaseConfig(conf.Common, false, false, services)), jsonString(conf))

		// check: saved config contains all base values.
		raw2, err := ioutil.ReadFile(filename) //nolint:gosec
		require.NoError(t, err)
		require.JSONEq(t, jsonString(MakeBaseConfig(conf.Common, false, false, services)), string(raw2))
	})
}
