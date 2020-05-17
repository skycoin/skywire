package visorconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"sync"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
)

const (
	// StdinName is the path name used to identify STDIN.
	StdinName = "STDIN"
)

var (
	// ErrNoConfigPath is returned on attempt to read/write config when visor contains no config path.
	ErrNoConfigPath = errors.New("no config path")
)

// Common represents the common fields that are shared across all config versions,
// alongside logging and flushing fields.
type Common struct {
	path string
	log  *logging.MasterLogger
	mu   sync.RWMutex

	Version string        `json:"version"`
	SK      cipher.SecKey `json:"sk,omitempty"`
	pk      cipher.PubKey
}

// NewCommon returns a new Common.
func NewCommon(log *logging.MasterLogger, confPath string, version string, sk *cipher.SecKey) (*Common, error) {
	if log == nil {
		log = logging.NewMasterLogger()
	}

	c := new(Common)
	c.log = log
	c.path = confPath
	c.Version = version
	if sk != nil {
		c.SK = *sk
		if _, err := c.ensureKeys(); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Flush flushes config to file.
func (c *Common) Flush() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.flush()
}

func (c *Common) flush() error {
	switch c.path {
	case "":
		return ErrNoConfigPath
	case StdinName:
		return nil
	}

	j, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}
	c.log.Debugf("Updating visor config to: %s", string(j))

	bytes, err := json.MarshalIndent(c, "", "\t")
	if err != nil {
		return err
	}
	const filePerm = 0644
	return ioutil.WriteFile(c.path, bytes, filePerm)
}

// PK returns the visor's public key.
func (c *Common) PK() cipher.PubKey {
	if _, err := c.ensureKeys(); err != nil {
		panic(fmt.Errorf("invalid 'sk' defined in visor config: %w", err))
	}
	return c.pk
}

func (c *Common) ensureKeys() (changed bool, err error) {
	if !c.pk.Null() {
		return false, nil
	}
	if c.SK.Null() {
		c.pk, c.SK = cipher.GenerateKeyPair()
		return true, nil
	}
	if c.pk, err = c.SK.PubKey(); err != nil {
		return false, err
	}
	return true, nil
}

// MasterLogger returns the underlying master logger.
func (c *Common) MasterLogger() *logging.MasterLogger {
	return c.log
}
