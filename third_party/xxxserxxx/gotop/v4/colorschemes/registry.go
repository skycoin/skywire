package colorschemes

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/shibukawa/configdir"
	"github.com/xxxserxxx/lingo/v2"
)

var registry map[string]Colorscheme

func init() {
	if registry == nil {
		registry = make(map[string]Colorscheme)
	}
}

var tr lingo.Translations

// Set the translation library
func SetTr(tra lingo.Translations) {
	tr = tra
}

// FromName loads a Colorscheme by name; confDir is used to search
// directories for a scheme matching the name.  The search order
// is the same as for config files.
func FromName(confDir configdir.ConfigDir, c string) (Colorscheme, error) {
	if c == "" {
		c = "default"
	}
	if cs, ok := registry[c]; ok {
		return cs, nil
	}
	cs, err := getCustomColorscheme(confDir, c)
	return cs, err
}

func register(name string, c Colorscheme) {
	if registry == nil {
		registry = make(map[string]Colorscheme)
	}
	c.Name = name
	registry[name] = c
}

// getCustomColorscheme	tries to read a custom json colorscheme from <configDir>/<name>.json
func getCustomColorscheme(confDir configdir.ConfigDir, name string) (Colorscheme, error) {
	var cs Colorscheme
	fn := name + ".json"
	folder := confDir.QueryFolderContainsFile(fn)
	if folder == nil {
		paths := make([]string, 0)
		for _, d := range confDir.QueryFolders(configdir.Existing) {
			paths = append(paths, d.Path)
		}
		return cs, errors.New(tr.Value("error.colorschemefile", fn, strings.Join(paths, ", ")))
	}
	dat, err := folder.ReadFile(fn)
	if err != nil {
		return cs, errors.New(tr.Value("error.colorschemeload", filepath.Join(folder.Path, fn), err.Error()))
	}
	err = json.Unmarshal(dat, &cs)
	if err != nil {
		return cs, errors.New(tr.Value("error.colorschemeparse", err.Error()))
	}
	return cs, nil
}
