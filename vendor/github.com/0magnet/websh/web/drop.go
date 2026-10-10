//go:build js && wasm

package web

import (
	"encoding/json"
	"path"
	"strconv"
	"strings"
	"syscall/js"

	"github.com/0magnet/websh/progressive"
)

// Files dropped onto the terminal (PROTOCOL.md): each is saved in the
// shell's filesystem, in ~/Downloads, and in the page's (jsfs, where a
// program run from the filesystem reads), and then, as a desktop terminal
// does, its path is typed where the cursor is: at the prompt, or to the
// running program. A program that asked (OSC 7337 listen;drop) gets a drop
// event instead, with the path, name, size and type.

// dropLimit is the largest file taken.
const dropLimit = 64 << 20

// wireDrop has the terminal's element take dropped files.
func (s *Session) wireDrop(el js.Value) {
	over := js.FuncOf(func(_ js.Value, args []js.Value) any {
		e := args[0]
		if t := e.Get("dataTransfer"); t.Truthy() && hasFiles(t) {
			e.Call("preventDefault")
			t.Set("dropEffect", "copy")
		}
		return nil
	})
	drop := js.FuncOf(func(_ js.Value, args []js.Value) any {
		e := args[0]
		t := e.Get("dataTransfer")
		if !t.Truthy() || !hasFiles(t) {
			return nil
		}
		e.Call("preventDefault")
		files := t.Get("files")
		for i := 0; i < files.Length(); i++ {
			s.dropFile(files.Index(i))
		}
		return nil
	})
	el.Call("addEventListener", "dragover", over)
	el.Call("addEventListener", "drop", drop)
}

func hasFiles(t js.Value) bool {
	types := t.Get("types")
	for i := 0; i < types.Length(); i++ {
		if types.Index(i).String() == "Files" {
			return true
		}
	}
	return false
}

// dropFile reads one file and saves it, then tells whoever is listening.
func (s *Session) dropFile(f js.Value) {
	size := f.Get("size").Int()
	name := cleanName(f.Get("name").String())
	if size > dropLimit {
		s.toast("Not saved: "+name, "it is larger than "+strconv.Itoa(dropLimit>>20)+" MB")
		return
	}
	var ok js.Func
	ok = js.FuncOf(func(_ js.Value, args []js.Value) any {
		ok.Release()
		buf := js.Global().Get("Uint8Array").New(args[0])
		b := make([]byte, buf.Length())
		js.CopyBytesToGo(b, buf)
		go s.dropped(name, f.Get("type").String(), b) // the shell's filesystem may block
		return nil
	})
	f.Call("arrayBuffer").Call("then", ok)
}

func (s *Session) dropped(name, mime string, b []byte) {
	home := "/home/user"
	if s.Shell.Runner != nil {
		if h := s.Shell.Runner.Env.Get("HOME"); h.IsSet() && h.String() != "" {
			home = h.String()
		}
	}
	dir := path.Join(home, "Downloads")
	p := uniquePath(s, dir, name)
	if err := s.Shell.FS.MkdirAll(dir, 0o755); err != nil {
		s.toast("Not saved: "+name, err.Error())
		return
	}
	if err := writeAll(s, p, b); err != nil {
		s.toast("Not saved: "+name, err.Error())
		return
	}
	if j := js.Global().Get("jsfs"); j.Truthy() { // where programs run from the filesystem read
		j.Call("mkdirp", dir)
		buf := js.Global().Get("Uint8Array").New(len(b))
		js.CopyBytesToJS(buf, b)
		j.Call("writeFile", p, buf)
	}
	if s.running && s.dropListen {
		data, err := json.Marshal(map[string]any{"path": p, "name": name, "size": len(b), "type": mime})
		if err == nil {
			s.event("drop", &progressive.Event{Type: "drop", Data: data})
		}
		return
	}
	typed := shellQuote(p) + " "
	if s.running {
		s.writeStdin([]byte(typed))
	} else {
		s.Editor.Input(typed)
	}
}

func uniquePath(s *Session, dir, name string) string {
	p := path.Join(dir, name)
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		if _, err := s.Shell.FS.Stat(p); err != nil {
			return p
		}
		p = path.Join(dir, base+"-"+strconv.Itoa(i)+ext)
	}
}

func writeAll(s *Session, p string, b []byte) error {
	f, err := s.Shell.FS.Create(p)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close() //nolint:errcheck,gosec // the write's error is the one to report
		return err
	}
	return f.Close()
}

// cleanName is a dropped file's name as a file name here: no directories,
// no control characters.
func cleanName(n string) string {
	n = strings.Map(func(r rune) rune {
		if r < ' ' || r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, n)
	if n == "" || n == "." || n == ".." {
		n = "dropped"
	}
	return n
}

// shellQuote quotes p for the shell, if it needs it.
func shellQuote(p string) string {
	if strings.IndexFunc(p, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-+,:@", r))
	}) < 0 {
		return p
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}
