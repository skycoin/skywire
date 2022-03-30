package dmsgpty

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
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
		CLIAddr:      DefaultCLIAddr(),
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
	if err = enc.Encode(&conf); err != nil {
		return err
	}
	return f.Close()
}

func findStringsEnclosedBy(str string, sep string, result []string, lastIndex int) ([]string, int) {
	s := strings.Index(str, sep)
	if s == -1 {
		return result, lastIndex
	}
	newS := str[s+len(sep):]
	e := strings.Index(newS, sep)
	if e == -1 {
		lastIndex += len(sep)
		return result, lastIndex
	}
	res := newS[:e]
	if res != "" {
		result = append(result, res)
	}
	last := s + len(res) + len(sep)
	if lastIndex == -1 {
		lastIndex = last
	} else {
		lastIndex += last
	}
	str = str[last:]
	return findStringsEnclosedBy(str, sep, result, lastIndex)
}

// ParseWindowsEnv finds '%'-enclosed windows env in json string
func ParseWindowsEnv(cliAddr string) string {
	if runtime.GOOS == "windows" {
		var res []string
		results, lastIndex := findStringsEnclosedBy(cliAddr, "%", res, -1)
		if len(results) > 0 {
			paths := make([]string, len(results)+1)
			for i, s := range results {
				pth := os.Getenv(strings.ToUpper(s))
				if pth != "" {
					paths[i] = pth
				}
			}
			paths[len(paths)-1] = strings.Replace(cliAddr[lastIndex:], string(filepath.Separator), "", 1)
			cliAddr = filepath.Join(paths...)
			_ = strings.ReplaceAll(cliAddr, `\`, `\\`)
			return cliAddr
		}
	}
	return cliAddr
}
