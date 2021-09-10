package dmsgpty

import (
	"fmt"
	"os"

	"github.com/skycoin/dmsg"
)

// Config struct is used to read the values from the config.json file
type Config struct {
	DmsgDisc     string   `json:"dmsgdisc"`
	DmsgSessions int      `json:"dmsgsessions"`
	DmsgPort     uint16   `json:"dmsgport"`
	CLINet       string   `json:"clinet"`
	CLIAddr      string   `json:"cliaddr"`
	SK           string   `json:"sk"`
	PK           string   `json:"pk"`
	WL           []string `json:"wl"`
}

// DefaultConfig is used to populate the config struct with its default values
func DefaultConfig() Config {
	return Config{
		DmsgDisc:     dmsg.DefaultDiscAddr,
		DmsgSessions: dmsg.DefaultMinSessions,
		DmsgPort:     DefaultPort,
		CLINet:       DefaultCLINet,
		CLIAddr:      DefaultCLIAddr,
	}
}

// WriteConfig write the config struct to the provided path
func WriteConfig(conf Config, path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644) //nolint:gosec
	if err != nil {
		return fmt.Errorf("failed to open config file: %w", err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "    ")
	return enc.Encode(&conf)
}
