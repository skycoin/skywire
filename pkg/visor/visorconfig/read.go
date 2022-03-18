package visorconfig

import (
"bytes"
"encoding/json"
"fmt"
"io/ioutil"
"os"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skycoin/src/util/logging"
)

const Pkgpath = "/opt/skywire/skywire.json"


func ReadConfig(path string) (*V1, error) {
	mLog := logging.NewMasterLogger()
	mLog.SetLevel(logrus.InfoLevel)

  f, err := os.ReadFile(path)
  if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	raw, err := ioutil.ReadAll(bytes.NewReader(f))
	if err != nil {
    return nil, fmt.Errorf("%w", err)
	}
  cc, err := NewCommon(mLog, path, "", nil)
  if err != nil {
		return nil, err
	}
  if err := json.Unmarshal(raw, cc); err != nil {
    		return nil, fmt.Errorf("failed to obtain config version: %w", err)
  }
  conf := MakeBaseConfig(cc)
  dec := json.NewDecoder(bytes.NewReader(raw))
  if err := dec.Decode(&conf); err != nil {
    		return nil, fmt.Errorf("failed to decode json: %w", err)
  }
  if err := conf.ensureKeys(); err != nil {
    		return nil, fmt.Errorf("%v: %w", ErrInvalidSK, err)
  }
return conf, err
}
