package shell

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/0magnet/sh/v3/interp"
	"github.com/0magnet/sh/v3/syntax"
)

// RCFile is the startup file an interactive websh sources from $HOME, as bash
// does ~/.bashrc.
const RCFile = ".webshrc"

// SourceRC runs $HOME/.webshrc in the shell's own interpreter, so what it
// defines stays for the session. A missing file is not an error. Hosts call
// it once, before the first prompt.
func (s *Shell) SourceRC(ctx context.Context) error {
	home := s.Var("HOME")
	if home == "" {
		return nil
	}
	name := path.Join(home, RCFile)
	f, err := s.FS.Open(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close() //nolint:errcheck // read only
	file, err := syntax.NewParser().Parse(f, name)
	if err != nil {
		return err
	}
	err = s.Runner.Run(ctx, file)
	if s.Runner.Exited() {
		s.Runner.Reset() // an exit in the rc does not end the session
	}
	var status interp.ExitStatus
	if errors.As(err, &status) {
		return nil // the status of its last command is not a failure to source it
	}
	return err
}

// Var is a shell variable's value, or "" when it is unset.
func (s *Shell) Var(name string) string {
	if v, ok := s.Runner.Vars[name]; ok {
		if v.IsSet() {
			return v.String()
		}
		return ""
	}
	return s.Runner.Env.Get(name).String()
}

// PS1 is the prompt PS1 asks for, with its escapes expanded, and whether PS1
// was set in the session. A host keeps its own prompt while it was not, so a
// value inherited from the environment, meant for another shell, is not used.
//
// The escapes are bash's: \u \h \H \w \W \$ \n \r \e \a \\ \t \T \@ \A \d,
// octal \nnn, and \[ \] around text that takes no cells, which come out as
// \x01 and \x02 for [LineEditor.Prompt]. $NAME and ${NAME} are expanded;
// command substitution is not run.
func (s *Shell) PS1() (string, bool) {
	ps1 := s.Var("PS1")
	if ps1 == s.ps1Start {
		return "", false
	}
	return s.expandPrompt(ps1), true
}

func (s *Shell) expandPrompt(p string) string {
	var b strings.Builder
	now := time.Now()
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c == '$' {
			name, n := promptVarName(p[i+1:])
			if n > 0 {
				b.WriteString(s.Var(name))
				i += n
				continue
			}
		}
		if c != '\\' || i+1 >= len(p) {
			b.WriteByte(c)
			continue
		}
		i++
		switch e := p[i]; e {
		case 'u':
			b.WriteString(s.Var("USER"))
		case 'h', 'H':
			host := s.Var("HOSTNAME")
			if host == "" {
				host, _ = os.Hostname() //nolint:errcheck // empty is the answer
			}
			if e == 'h' {
				host, _, _ = strings.Cut(host, ".")
			}
			b.WriteString(host)
		case 'w', 'W':
			dir := s.Dir()
			if home := s.Var("HOME"); home != "" && home != "/" && (dir == home || strings.HasPrefix(dir, home+"/")) {
				dir = "~" + dir[len(home):]
			}
			if e == 'W' && dir != "~" && dir != "/" {
				dir = path.Base(dir)
			}
			b.WriteString(dir)
		case '$':
			if s.Var("EUID") == "0" || s.Var("EUID") == "" && os.Geteuid() == 0 {
				b.WriteByte('#')
			} else {
				b.WriteByte('$')
			}
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 'e':
			b.WriteByte(0x1b)
		case 'a':
			b.WriteByte('\a')
		case '\\':
			b.WriteByte('\\')
		case '[':
			b.WriteByte(promptIgnoreStart)
		case ']':
			b.WriteByte(promptIgnoreEnd)
		case 't':
			b.WriteString(now.Format("15:04:05"))
		case 'T':
			b.WriteString(now.Format("03:04:05"))
		case '@':
			b.WriteString(now.Format("03:04 PM"))
		case 'A':
			b.WriteString(now.Format("15:04"))
		case 'd':
			b.WriteString(now.Format("Mon Jan 02"))
		case '0', '1', '2', '3':
			j := i
			for j < len(p) && j < i+3 && p[j] >= '0' && p[j] <= '7' {
				j++
			}
			n, _ := strconv.ParseUint(p[i:j], 8, 8) //nolint:errcheck // digits checked
			b.WriteByte(byte(n))
			i = j - 1
		default:
			b.WriteByte('\\')
			b.WriteByte(e)
		}
	}
	return b.String()
}

// promptVarName is the variable name at the start of s, as $NAME or ${NAME}
// leaves it, and how many bytes it took.
func promptVarName(s string) (string, int) {
	if strings.HasPrefix(s, "{") {
		if end := strings.IndexByte(s, '}'); end > 1 {
			return s[1:end], end + 1
		}
		return "", 0
	}
	n := 0
	for n < len(s) && (s[n] == '_' || s[n] >= 'a' && s[n] <= 'z' || s[n] >= 'A' && s[n] <= 'Z' || n > 0 && s[n] >= '0' && s[n] <= '9') {
		n++
	}
	return s[:n], n
}
