package shell

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/0magnet/afero"
)

// Builtins are the interpreter's own commands, which completion offers
// alongside the applets.
var Builtins = []string{
	"cd", "pwd", "echo", "printf", "read", "exit", "export", "unset",
	"source", "test", "true", "false", "set", "shift", "local",
	"declare", "eval", "alias", "unalias", "type", "return", "break",
	"continue", "pushd", "popd", "dirs", "let", "getopts", "wait",
	"jobs", "kill", "disown", "fg", "bg", "enable", "compgen", "history",
	"builtin", "umask", "times", "trap", "shopt", "mapfile", "readarray",
}

// Complete returns the candidates for the word being completed, sorted: a
// command name when isFirstWord, and otherwise a path, with directories ending
// in "/". It is what a line editor's Complete is set to.
//
// A command name is an applet or a builtin anywhere, and on the OS also a
// program on the PATH, since those run too.
func (s *Shell) Complete(word string, isFirstWord bool) []string {
	if isFirstWord && !strings.Contains(word, "/") {
		seen := map[string]bool{}
		var names []string
		add := func(n string) {
			if strings.HasPrefix(n, word) && !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
		for _, n := range AppletNames() {
			add(n)
		}
		for _, n := range Builtins {
			add(n)
		}
		if s.native {
			for _, n := range s.pathPrograms(word) {
				add(n)
			}
		}
		sort.Strings(names)
		return names
	}
	dir, base := filepath.Split(word)
	search := dir
	if strings.HasPrefix(search, "~/") && s.native {
		search = filepath.Join(s.home(), search[2:])
	}
	if !filepath.IsAbs(search) {
		search = filepath.Join(s.Dir(), search)
	}
	infos, err := afero.ReadDir(s.FS, filepath.Clean(search))
	if err != nil {
		return nil
	}
	var names []string
	for _, info := range infos {
		if !strings.HasPrefix(info.Name(), base) {
			continue
		}
		cand := dir + info.Name()
		if info.IsDir() {
			cand += "/"
		}
		names = append(names, cand)
	}
	sort.Strings(names)
	return names
}

// pathPrograms is every executable on the shell's PATH whose name starts with
// prefix.
func (s *Shell) pathPrograms(prefix string) []string {
	var names []string
	for _, d := range filepath.SplitList(s.Runner.Env.Get("PATH").String()) {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), prefix) || e.IsDir() {
				continue
			}
			if info, err := e.Info(); err == nil && info.Mode()&0o111 != 0 {
				names = append(names, e.Name())
			}
		}
	}
	return names
}

// home is the shell's $HOME.
func (s *Shell) home() string {
	return s.Runner.Env.Get("HOME").String()
}
