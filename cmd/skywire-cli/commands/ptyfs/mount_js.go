//go:build js && wasm

// Package cliptyfs cmd/skywire-cli/commands/ptyfs/mount_js.go c4-vis-cli
//
// In a browser visor there is no FUSE: the filesystem every program on the
// page shares is bottle's jsfs, and jsfs.mount hands a subtree of it to a
// provider. mount serves that subtree from the peer's sftp subsystem, reached
// through the local visor like the Linux via-visor path. Every provider call
// answers from a goroutine, never from the callback, so the page is never
// held while a request crosses the network.
package cliptyfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sync"
	"syscall/js"
	"time"

	"github.com/pkg/sftp"
	"github.com/spf13/cobra"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/pty"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	mountReadOnly   bool
	mountRemoteRoot string
)

func init() {
	mountCmd.Flags().BoolVar(&mountReadOnly, "ro", false, "mount read-only (writes return EROFS)")
	mountCmd.Flags().StringVar(&mountRemoteRoot, "remote-root", "/", "absolute path on the remote to expose as the mount root")
	RootCmd.AddCommand(mountCmd, umountCmd)
}

var mountCmd = &cobra.Command{
	Use:   "mount <pk> <dir>",
	Short: "Mount a peer visor's filesystem into this page's filesystem",
	Long: `mount serves <dir> in this browser visor's filesystem from the peer's
sftp subsystem, reached through the local visor. Every program on the page
sees it: ls, cat and skywire commands alike. It runs until 'pty fs umount
<dir>' or Ctrl-C.`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		var rPK cipher.PubKey
		if err := rPK.Set(args[0]); err != nil {
			return fmt.Errorf("ptyfs: %q is not a public key: %w", args[0], err)
		}
		dir, err := filepath.Abs(args[1])
		if err != nil {
			return err
		}
		jsfs := js.Global().Get("jsfs")
		if !jsfs.Truthy() || jsfs.Get("mount").Type() != js.TypeFunction {
			return errors.New("ptyfs: this page's filesystem cannot mount (jsfs.mount missing)")
		}
		m := &browserMount{pk: rPK, root: mountRemoteRoot, ro: mountReadOnly, files: map[int]*mountedFile{}}
		if _, err := m.client(); err != nil {
			return err
		}
		defer m.close()

		unmounted := make(chan struct{})
		provider, release := m.provider(func() { close(unmounted) })
		defer release()
		if err := callJS(jsfs, "mount", dir, provider); err != nil {
			return fmt.Errorf("ptyfs: mount %s: %w", dir, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "ptyfs: mounted %s -> %s (remote-root=%s)\n", dir, rPK, mountRemoteRoot) //nolint:errcheck

		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		// A pending timer, not a bare select: jsfs answers on a microtask
		// and the Go scheduler must not decide every goroutine is asleep.
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-unmounted:
				return nil
			case <-ctx.Done():
				_ = callJS(jsfs, "unmount", dir) //nolint:errcheck
				return nil
			case <-tick.C:
			}
		}
	},
}

var umountCmd = &cobra.Command{
	Use:          "umount <dir>",
	Short:        "Unmount a pty fs mount",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(_ *cobra.Command, args []string) error {
		dir, err := filepath.Abs(args[0])
		if err != nil {
			return err
		}
		return callJS(js.Global().Get("jsfs"), "unmount", dir)
	},
}

// callJS calls obj[name](args...) and turns a thrown error into a Go one.
func callJS(obj js.Value, name string, args ...any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if je, ok := r.(js.Error); ok {
				err = errors.New(je.Get("message").String())
				return
			}
			err = fmt.Errorf("%v", r)
		}
	}()
	obj.Call(name, args...)
	return nil
}

// browserMount is one mount: the sftp session (redialed once when it drops)
// and the files open through it.
type browserMount struct {
	pk   cipher.PubKey
	root string
	ro   bool

	mu    sync.Mutex
	c     *sftp.Client
	gen   int // bumped per session; a file opened in an older one is gone
	files map[int]*mountedFile
	next  int
}

type mountedFile struct {
	f   *sftp.File // nil for a directory
	p   string
	pos int64
	gen int
}

func (m *browserMount) client() (*sftp.Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.c != nil {
		return m.c, nil
	}
	conn, err := clirpc.BridgeConn(0 /* dmsg */, m.pk, skyenv.DmsgPtyPort)
	if err != nil {
		return nil, fmt.Errorf("ptyfs: bridge through the local visor: %w", err)
	}
	rwc, err := pty.OpenSftpConn(conn)
	if err != nil {
		_ = conn.Close() //nolint:errcheck
		return nil, err
	}
	c, err := sftp.NewClientPipe(rwc, rwc)
	if err != nil {
		_ = rwc.Close() //nolint:errcheck
		return nil, fmt.Errorf("ptyfs: sftp: %w", err)
	}
	m.c = c
	m.gen++
	return c, nil
}

// lost drops a session that failed, so the next call dials a new one.
func (m *browserMount) lost(c *sftp.Client) {
	m.mu.Lock()
	if m.c == c {
		m.c = nil
	}
	m.mu.Unlock()
	_ = c.Close() //nolint:errcheck
}

func (m *browserMount) close() {
	m.mu.Lock()
	c := m.c
	m.c = nil
	m.mu.Unlock()
	if c != nil {
		_ = c.Close() //nolint:errcheck
	}
}

// sessionGone reports an error that means the connection, not the request,
// failed.
func sessionGone(err error) bool {
	return errors.Is(err, sftp.ErrSSHFxConnectionLost) || errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, os.ErrClosed)
}

// do runs fn on the session, redialing once if the session was lost.
func (m *browserMount) do(fn func(c *sftp.Client) (any, error)) (any, error) {
	for attempt := 0; ; attempt++ {
		c, err := m.client()
		if err != nil {
			return nil, err
		}
		res, err := fn(c)
		if err != nil && sessionGone(err) && attempt == 0 {
			m.lost(c)
			continue
		}
		return res, err
	}
}

func (m *browserMount) remote(rel string) string {
	return path.Join(m.root, rel)
}

// provider builds the jsfs provider object. release frees its callbacks.
func (m *browserMount) provider(onUnmount func()) (js.Value, func()) {
	obj := js.Global().Get("Object").New()
	var funcs []js.Func
	def := func(name string, fn func(args []js.Value) (any, error)) {
		f := js.FuncOf(func(_ js.Value, a []js.Value) any {
			cb := a[len(a)-1]
			args := a[:len(a)-1]
			go func() {
				res, err := fn(args)
				if err != nil {
					cb.Invoke(jsErr(err))
					return
				}
				cb.Invoke(js.Null(), res)
			}()
			return nil
		})
		funcs = append(funcs, f)
		obj.Set(name, f)
	}
	writes := func(name string, fn func(args []js.Value) (any, error)) {
		def(name, func(args []js.Value) (any, error) {
			if m.ro {
				return nil, syscallErr{"EROFS", "read-only mount"}
			}
			return fn(args)
		})
	}

	stat := func(lstat bool) func([]js.Value) (any, error) {
		return func(a []js.Value) (any, error) {
			return m.do(func(c *sftp.Client) (any, error) {
				var fi os.FileInfo
				var err error
				if lstat {
					fi, err = c.Lstat(m.remote(a[0].String()))
				} else {
					fi, err = c.Stat(m.remote(a[0].String()))
				}
				if err != nil {
					return nil, err
				}
				return statJS(fi), nil
			})
		}
	}
	def("stat", stat(false))
	def("lstat", stat(true))
	def("readdir", func(a []js.Value) (any, error) {
		return m.do(func(c *sftp.Client) (any, error) {
			ents, err := c.ReadDir(m.remote(a[0].String()))
			if err != nil {
				return nil, err
			}
			names := make([]any, len(ents))
			for i, e := range ents {
				names[i] = e.Name()
			}
			return js.ValueOf(names), nil
		})
	})
	def("readlink", func(a []js.Value) (any, error) {
		return m.do(func(c *sftp.Client) (any, error) { return c.ReadLink(m.remote(a[0].String())) })
	})
	def("open", func(a []js.Value) (any, error) { return m.open(a[0].String(), a[1].Int()) })
	def("close", func(a []js.Value) (any, error) {
		m.mu.Lock()
		mf := m.files[a[0].Int()]
		delete(m.files, a[0].Int())
		m.mu.Unlock()
		if mf != nil && mf.f != nil {
			_ = mf.f.Close() //nolint:errcheck
		}
		return js.Undefined(), nil
	})
	def("fstat", func(a []js.Value) (any, error) {
		mf, err := m.file(a[0].Int())
		if err != nil {
			return nil, err
		}
		if mf.f == nil {
			return m.do(func(c *sftp.Client) (any, error) {
				fi, err := c.Stat(mf.p)
				if err != nil {
					return nil, err
				}
				return statJS(fi), nil
			})
		}
		fi, err := mf.f.Stat()
		if err != nil {
			return nil, err
		}
		return statJS(fi), nil
	})
	def("read", func(a []js.Value) (any, error) {
		mf, err := m.file(a[0].Int())
		if err != nil {
			return nil, err
		}
		if mf.f == nil {
			return nil, syscallErr{"EISDIR", mf.p}
		}
		buf := make([]byte, a[1].Int())
		at := mf.pos
		if a[2].Type() == js.TypeNumber {
			at = int64(a[2].Int())
		}
		n, err := mf.f.ReadAt(buf, at)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if a[2].Type() != js.TypeNumber {
			mf.pos += int64(n)
		}
		u := js.Global().Get("Uint8Array").New(n)
		js.CopyBytesToJS(u, buf[:n])
		return u, nil
	})
	writes("write", func(a []js.Value) (any, error) {
		mf, err := m.file(a[0].Int())
		if err != nil {
			return nil, err
		}
		if mf.f == nil {
			return nil, syscallErr{"EISDIR", mf.p}
		}
		buf := make([]byte, a[1].Get("length").Int())
		js.CopyBytesToGo(buf, a[1])
		at := mf.pos
		if a[2].Type() == js.TypeNumber {
			at = int64(a[2].Int())
		}
		n, err := mf.f.WriteAt(buf, at)
		if err != nil {
			return nil, err
		}
		if a[2].Type() != js.TypeNumber {
			mf.pos += int64(n)
		}
		return n, nil
	})
	writes("ftruncate", func(a []js.Value) (any, error) {
		mf, err := m.file(a[0].Int())
		if err != nil {
			return nil, err
		}
		if mf.f == nil {
			return nil, syscallErr{"EISDIR", mf.p}
		}
		return js.Undefined(), mf.f.Truncate(int64(a[1].Int()))
	})
	def("fsync", func([]js.Value) (any, error) { return js.Undefined(), nil })

	pathOp := func(name string, fn func(c *sftp.Client, a []js.Value) error) {
		writes(name, func(a []js.Value) (any, error) {
			return m.do(func(c *sftp.Client) (any, error) { return js.Undefined(), fn(c, a) })
		})
	}
	pathOp("mkdir", func(c *sftp.Client, a []js.Value) error { return c.Mkdir(m.remote(a[0].String())) })
	pathOp("rmdir", func(c *sftp.Client, a []js.Value) error { return c.RemoveDirectory(m.remote(a[0].String())) })
	pathOp("unlink", func(c *sftp.Client, a []js.Value) error { return c.Remove(m.remote(a[0].String())) })
	pathOp("rename", func(c *sftp.Client, a []js.Value) error {
		from, to := m.remote(a[0].String()), m.remote(a[1].String())
		if err := c.PosixRename(from, to); err == nil || sessionGone(err) {
			return err
		}
		return c.Rename(from, to)
	})
	pathOp("link", func(c *sftp.Client, a []js.Value) error {
		return c.Link(m.remote(a[0].String()), m.remote(a[1].String()))
	})
	pathOp("symlink", func(c *sftp.Client, a []js.Value) error {
		return c.Symlink(a[0].String(), m.remote(a[1].String()))
	})
	pathOp("truncate", func(c *sftp.Client, a []js.Value) error {
		return c.Truncate(m.remote(a[0].String()), int64(a[1].Int()))
	})
	pathOp("chmod", func(c *sftp.Client, a []js.Value) error {
		return c.Chmod(m.remote(a[0].String()), os.FileMode(a[1].Int()&0o7777)) //nolint:gosec
	})
	for _, name := range []string{"chown", "lchown"} {
		pathOp(name, func(c *sftp.Client, a []js.Value) error {
			return c.Chown(m.remote(a[0].String()), a[1].Int(), a[2].Int())
		})
	}
	pathOp("utimes", func(c *sftp.Client, a []js.Value) error {
		sec := func(v js.Value) time.Time { return time.UnixMilli(int64(v.Float() * 1000)) }
		return c.Chtimes(m.remote(a[0].String()), sec(a[1]), sec(a[2]))
	})

	unF := js.FuncOf(func(js.Value, []js.Value) any { go onUnmount(); return nil })
	funcs = append(funcs, unF)
	obj.Set("unmounted", unF)

	return obj, func() {
		for _, f := range funcs {
			f.Release()
		}
	}
}

// open opens rel with jsfs's (Linux-valued) open flags. A directory gets a
// handle with no sftp file: sftp cannot open one, and Go reads a directory
// through readdir on its path anyway.
func (m *browserMount) open(rel string, flags int) (any, error) {
	p := m.remote(rel)
	const (
		oWronly, oRdwr, oCreat, oExcl, oTrunc, oAppend = 0o1, 0o2, 0o100, 0o200, 0o1000, 0o2000
	)
	writing := flags&(oWronly|oRdwr|oCreat|oTrunc|oAppend) != 0
	if writing && m.ro {
		return nil, syscallErr{"EROFS", "read-only mount"}
	}
	res, err := m.do(func(c *sftp.Client) (any, error) {
		if flags&oCreat == 0 {
			if fi, err := c.Stat(p); err == nil && fi.IsDir() {
				return &mountedFile{p: p}, nil
			}
		}
		of := os.O_RDONLY
		switch {
		case flags&oRdwr != 0:
			of = os.O_RDWR
		case flags&oWronly != 0:
			of = os.O_WRONLY
		}
		for bit, o := range map[int]int{oCreat: os.O_CREATE, oExcl: os.O_EXCL, oTrunc: os.O_TRUNC, oAppend: os.O_APPEND} {
			if flags&bit != 0 {
				of |= o
			}
		}
		f, err := c.OpenFile(p, of)
		if err != nil {
			return nil, err
		}
		mf := &mountedFile{f: f, p: p}
		if of&os.O_APPEND != 0 {
			if fi, err := f.Stat(); err == nil {
				mf.pos = fi.Size()
			}
		}
		return mf, nil
	})
	if err != nil {
		return nil, err
	}
	mf := res.(*mountedFile) //nolint:errcheck,forcetypeassert
	m.mu.Lock()
	m.next++
	h := m.next
	mf.gen = m.gen
	m.files[h] = mf
	m.mu.Unlock()
	return h, nil
}

// file returns an open file, or EIO when its session has since been lost.
func (m *browserMount) file(h int) (*mountedFile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mf := m.files[h]
	if mf == nil {
		return nil, syscallErr{"EBADF", "bad handle"}
	}
	if mf.f != nil && mf.gen != m.gen {
		return nil, syscallErr{"EIO", "the connection to the peer was lost since this file was opened"}
	}
	return mf, nil
}

// statJS renders fi the way Go's js/wasm runtime reads a stat result.
func statJS(fi os.FileInfo) js.Value {
	mode := uint32(fi.Mode().Perm())
	switch {
	case fi.IsDir():
		mode |= 0o040000
	case fi.Mode()&fs.ModeSymlink != 0:
		mode |= 0o120000
	default:
		mode |= 0o100000
	}
	var uid, gid uint32
	atime, mtime := fi.ModTime(), fi.ModTime()
	if st, ok := fi.Sys().(*sftp.FileStat); ok {
		uid, gid = st.UID, st.GID
		atime = time.Unix(int64(st.Atime), 0)
	}
	size := fi.Size()
	return js.ValueOf(map[string]any{
		"dev": 0, "ino": 0, "mode": mode, "nlink": 1, "uid": uid, "gid": gid, "rdev": 0,
		"size": size, "blksize": 4096, "blocks": (size + 511) / 512,
		"atimeMs": atime.UnixMilli(), "mtimeMs": mtime.UnixMilli(), "ctimeMs": mtime.UnixMilli(),
	})
}

// syscallErr is an error with the errno name jsfs and Go's runtime expect.
type syscallErr struct{ code, msg string }

func (e syscallErr) Error() string { return e.code + ": " + e.msg }

// jsErr turns err into the {code, message} jsfs passes back to the caller.
func jsErr(err error) js.Value {
	code := "EIO"
	var se syscallErr
	var st *sftp.StatusError
	switch {
	case errors.As(err, &se):
		code = se.code
	case errors.Is(err, fs.ErrNotExist):
		code = "ENOENT"
	case errors.Is(err, fs.ErrExist):
		code = "EEXIST"
	case errors.Is(err, fs.ErrPermission):
		code = "EACCES"
	case errors.As(err, &st):
		switch st.FxCode() {
		case sftp.ErrSSHFxNoSuchFile:
			code = "ENOENT"
		case sftp.ErrSSHFxPermissionDenied:
			code = "EACCES"
		case sftp.ErrSSHFxOpUnsupported:
			code = "ENOSYS"
		}
	}
	return js.ValueOf(map[string]any{"code": code, "message": err.Error()})
}
