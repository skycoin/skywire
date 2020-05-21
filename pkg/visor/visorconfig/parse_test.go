package visorconfig

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/stretchr/testify/require"
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
		raw := []byte(fmt.Sprintf(`{"version":"%s","sk":"%s"}`, V1Name, sk.String()))
		n, err := f.Write(raw)
		require.NoError(t, err)
		require.Len(t, raw, n)
		require.NoError(t, f.Close())

		// check: obtained config contains all base values.
		conf, err := Parse(nil, filename, raw)
		require.NoError(t, err)
		require.JSONEq(t, jsonString(MakeBaseConfig(conf.Common)), jsonString(conf))

		// check: saved config contains all base values.
		raw2, err := ioutil.ReadFile(filename) //nolint:gosec
		require.NoError(t, err)
		require.JSONEq(t, jsonString(MakeBaseConfig(conf.Common)), string(raw2))
	})
}
