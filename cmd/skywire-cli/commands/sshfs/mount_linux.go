//go:build linux

// Package clisshfs cmd/skywire-cli/commands/sshfs/mount_linux.go —
// FUSE mount + unmount for the sshfs subsystem. Maps each FUSE op
// onto the matching github.com/pkg/sftp call.
//
// Layering: dmsgpty-tcp noise stream  →  sftp.Client  →  sftpNode
// (fs.InodeEmbedder)  →  hanwen/go-fuse server  →  kernel.
//
// Scope for v1: rw mount of the remote process's filesystem view as
// seen by the host's user (no chroot, no per-mount allowlist) — same
// gating model as the interactive pty subsystem. Symlinks are
// followed via Readlink; xattrs / fifos / sockets / devices are not
// exposed (the standard sshfs feature set; we can extend in a
// follow-up if a use case shows up).
package clisshfs

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/pkg/sftp"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/pty"
)

// mountCmd flag storage.
var (
	mountReadOnly   bool
	mountDebug      bool
	mountAttrTTL    time.Duration
	mountEntryTTL   time.Duration
	mountRemoteRoot string
)

func init() {
	mountCmd.Flags().SortFlags = false
	mountCmd.Flags().BoolVar(&mountReadOnly, "ro", false,
		"mount read-only (writes return EROFS)")
	mountCmd.Flags().BoolVar(&mountDebug, "debug", false,
		"verbose FUSE debug logging to stderr")
	mountCmd.Flags().DurationVar(&mountAttrTTL, "attr-ttl", 1*time.Second,
		"how long the kernel may cache stat results (longer = faster ls/stat, staler attrs)")
	mountCmd.Flags().DurationVar(&mountEntryTTL, "entry-ttl", 1*time.Second,
		"how long the kernel may cache directory-entry lookups")
	mountCmd.Flags().StringVar(&mountRemoteRoot, "remote-root", "/",
		"absolute path on the remote to expose as the mount root")
	RootCmd.AddCommand(mountCmd, umountCmd)
}

var mountCmd = &cobra.Command{
	Use:   "mount <pk>@<host>:<port> <mountpoint>",
	Short: "Mount a peer visor's filesystem over the sftp subsystem",
	Long: `mount opens a noise-XK protected TCP connection to the peer's
dmsgpty endpoint, requests the sftp subsystem, and exposes the
remote filesystem at <mountpoint> via FUSE.

Runs in the foreground. Send SIGINT (Ctrl-C) or SIGTERM to unmount
and exit. For a background mount use 'nohup ... &' or a systemd
unit — there is no daemonize flag by design (operators can compose
their own supervision).`,
	Args:                  cobra.ExactArgs(2),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		dest := injectDefaultPort(args[0], sshfsDefPort)
		mountpoint := args[1]

		rPK, addr, err := parseSSHFSDestination(dest)
		if err != nil {
			return err
		}
		myPK, mySK := resolveSSHFSIdentity(sshfsSK, !sshfsNoVisor)

		// Pre-flight the mountpoint so the FUSE error doesn't bury the
		// real cause ("does not exist" / "not a directory").
		if st, err := os.Stat(mountpoint); err != nil {
			return fmt.Errorf("sshfs: mountpoint %q: %w", mountpoint, err)
		} else if !st.IsDir() {
			return fmt.Errorf("sshfs: mountpoint %q is not a directory", mountpoint)
		}

		ctx, cancel := cmdutil.SignalContext(cmd.Context(), nil)
		defer cancel()

		return runMount(ctx, addr, rPK, myPK, mySK, mountpoint)
	},
}

var umountCmd = &cobra.Command{
	Use:                   "umount <mountpoint>",
	Short:                 "Unmount a previously-mounted sshfs (calls fusermount -u)",
	Args:                  cobra.ExactArgs(1),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	RunE: func(_ *cobra.Command, args []string) error {
		out, err := exec.Command("fusermount", "-u", args[0]).CombinedOutput() //nolint:gosec // fusermount is a fixed system binary; only the user-supplied mountpoint flows in as an arg, same trust model as cli ssh / cli sshfs mount
		if err != nil {
			return fmt.Errorf("sshfs umount %s: %w (%s)", args[0], err, string(out))
		}
		return nil
	},
}

// runMount wires the dmsgpty-tcp stream → sftp.Client → FUSE mount
// and blocks until ctx fires (Ctrl-C). On exit it unmounts cleanly
// via Server.Unmount() before returning.
func runMount(
	ctx context.Context,
	addr string,
	rPK cipher.PubKey,
	myPK cipher.PubKey, mySK cipher.SecKey,
	mountpoint string,
) error {
	rwc, err := pty.DialSftpTCP(ctx, addr, myPK, mySK, rPK)
	if err != nil {
		return err
	}

	// sftp.NewClientPipe takes the read + write halves separately. Our
	// stream is a single io.ReadWriteCloser; reuse it for both halves.
	sftpC, err := sftp.NewClientPipe(rwc, rwc)
	if err != nil {
		_ = rwc.Close() //nolint:errcheck,gosec
		return fmt.Errorf("sshfs: sftp handshake: %w", err)
	}
	defer sftpC.Close() //nolint:errcheck

	root := &sftpNode{
		root: &sftpRoot{cli: sftpC, remoteRoot: path.Clean(mountpoint_remote_default(mountRemoteRoot))},
	}

	opts := &fs.Options{
		AttrTimeout:  &mountAttrTTL,
		EntryTimeout: &mountEntryTTL,
	}
	opts.MountOptions.Debug = mountDebug
	opts.MountOptions.Name = "skywire-sshfs"
	opts.MountOptions.FsName = rPK.String()
	if mountReadOnly {
		opts.MountOptions.Options = append(opts.MountOptions.Options, "ro")
	}

	server, err := fs.Mount(mountpoint, root, opts)
	if err != nil {
		return fmt.Errorf("sshfs: FUSE mount %s: %w", mountpoint, err)
	}
	fmt.Fprintf(os.Stderr, "sshfs: mounted %s -> %s@%s (remote-root=%s)\n", mountpoint, rPK, addr, root.root.remoteRoot) //nolint:errcheck

	// Either the caller's ctx fires (Ctrl-C) or the kernel unmounts
	// out from under us (e.g. external `fusermount -u`). Wait for
	// either + tear down cleanly.
	done := make(chan struct{})
	go func() {
		server.Wait()
		close(done)
	}()

	// Reinforce signal handling in case the parent cmd's signal
	// context doesn't catch SIGTERM cleanly under exec wrappers.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case <-ctx.Done():
	case <-sigCh:
	case <-done:
		return nil
	}
	if err := server.Unmount(); err != nil {
		fmt.Fprintf(os.Stderr, "sshfs: unmount returned: %v (falling back to fusermount -u)\n", err) //nolint:errcheck
		_, _ = exec.Command("fusermount", "-u", mountpoint).CombinedOutput()                         //nolint:errcheck,gosec // fixed system binary, mountpoint is the operator-supplied path we just unmounted
	}
	return nil
}

// mountpoint_remote_default keeps the explicit "/" default obvious in
// runMount; if the user passes a relative remote-root we still serve
// that path verbatim (sftp resolves it relative to the user's home).
func mountpoint_remote_default(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

// sftpRoot is the per-mount state shared by every sftpNode in the
// tree: the live sftp.Client + the remote root that node paths are
// rooted at.
type sftpRoot struct {
	cli        *sftp.Client
	remoteRoot string
}

// sftpNode is one inode in the FUSE tree. relPath is the path
// relative to the mount's remoteRoot; the absolute remote path is
// joined on demand to keep the node small and to make rename / move
// work without rewriting every descendant.
type sftpNode struct {
	fs.Inode
	root    *sftpRoot
	relPath string
}

// remotePath returns the absolute remote path this inode refers to.
func (n *sftpNode) remotePath() string {
	if n.relPath == "" {
		return n.root.remoteRoot
	}
	return path.Join(n.root.remoteRoot, n.relPath)
}

// childRelPath joins this node's relPath with name without leaving
// the relative-root invariant intact.
func (n *sftpNode) childRelPath(name string) string {
	if n.relPath == "" {
		return name
	}
	return path.Join(n.relPath, name)
}

// fillAttrFromFileInfo copies sftp.FileInfo attrs into a fuse.Attr.
func fillAttrFromFileInfo(fi os.FileInfo, a *fuse.Attr) {
	a.Mode = uint32(fi.Mode().Perm())
	switch {
	case fi.Mode().IsDir():
		a.Mode |= syscall.S_IFDIR
	case fi.Mode()&os.ModeSymlink != 0:
		a.Mode |= syscall.S_IFLNK
	default:
		a.Mode |= syscall.S_IFREG
	}
	a.Size = uint64(fi.Size()) //nolint:gosec // os.FileInfo.Size is int64, FUSE attr expects uint64; negative sizes don't occur for valid files
	mtime := fi.ModTime()
	a.Mtime = uint64(mtime.Unix())           //nolint:gosec // unix epoch fits uint64 well past year 9999
	a.Mtimensec = uint32(mtime.Nanosecond()) //nolint:gosec // Nanosecond is [0, 1e9), fits uint32
	a.Atime = a.Mtime
	a.Atimensec = a.Mtimensec
	a.Ctime = a.Mtime
	a.Ctimensec = a.Mtimensec
}

var (
	_ fs.NodeLookuper   = (*sftpNode)(nil)
	_ fs.NodeGetattrer  = (*sftpNode)(nil)
	_ fs.NodeReaddirer  = (*sftpNode)(nil)
	_ fs.NodeOpener     = (*sftpNode)(nil)
	_ fs.NodeCreater    = (*sftpNode)(nil)
	_ fs.NodeMkdirer    = (*sftpNode)(nil)
	_ fs.NodeUnlinker   = (*sftpNode)(nil)
	_ fs.NodeRmdirer    = (*sftpNode)(nil)
	_ fs.NodeRenamer    = (*sftpNode)(nil)
	_ fs.NodeReadlinker = (*sftpNode)(nil)
	_ fs.NodeSetattrer  = (*sftpNode)(nil)
)

func (n *sftpNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	rel := n.childRelPath(name)
	abs := path.Join(n.root.remoteRoot, rel)
	fi, err := n.root.cli.Lstat(abs)
	if err != nil {
		return nil, sftpToErrno(err)
	}
	fillAttrFromFileInfo(fi, &out.Attr)
	child := &sftpNode{root: n.root, relPath: rel}
	stable := fs.StableAttr{Mode: out.Attr.Mode & syscall.S_IFMT}
	return n.NewInode(ctx, child, stable), 0
}

func (n *sftpNode) Getattr(_ context.Context, _ fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	fi, err := n.root.cli.Lstat(n.remotePath())
	if err != nil {
		return sftpToErrno(err)
	}
	fillAttrFromFileInfo(fi, &out.Attr)
	return 0
}

func (n *sftpNode) Readdir(_ context.Context) (fs.DirStream, syscall.Errno) {
	entries, err := n.root.cli.ReadDir(n.remotePath())
	if err != nil {
		return nil, sftpToErrno(err)
	}
	list := make([]fuse.DirEntry, 0, len(entries))
	for _, e := range entries {
		var typ uint32
		switch {
		case e.IsDir():
			typ = syscall.S_IFDIR
		case e.Mode()&os.ModeSymlink != 0:
			typ = syscall.S_IFLNK
		default:
			typ = syscall.S_IFREG
		}
		list = append(list, fuse.DirEntry{
			Mode: typ,
			Name: e.Name(),
		})
	}
	return fs.NewListDirStream(list), 0
}

func (n *sftpNode) Open(_ context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	f, err := n.root.cli.OpenFile(n.remotePath(), int(flags))
	if err != nil {
		return nil, 0, sftpToErrno(err)
	}
	return &sftpHandle{f: f}, 0, 0
}

func (n *sftpNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	rel := n.childRelPath(name)
	abs := path.Join(n.root.remoteRoot, rel)
	// Caller's flags already carry the access mode (O_RDONLY/O_WRONLY/
	// O_RDWR in the low 2 bits) and likely O_TRUNC; we only need to
	// ensure O_CREATE is on. Forcing O_RDWR alongside an existing
	// O_WRONLY would OR access-mode bits 1|2=3, which is undefined.
	// pkg/sftp's OpenFile takes no mode arg — Chmod afterwards applies
	// the requested perms.
	f, err := n.root.cli.OpenFile(abs, int(flags)|os.O_CREATE)
	if err != nil {
		return nil, nil, 0, sftpToErrno(err)
	}
	if mode != 0 {
		_ = n.root.cli.Chmod(abs, os.FileMode(mode&0o7777)) //nolint:errcheck,gosec
	}
	fi, err := n.root.cli.Lstat(abs)
	if err != nil {
		_ = f.Close() //nolint:errcheck,gosec
		return nil, nil, 0, sftpToErrno(err)
	}
	fillAttrFromFileInfo(fi, &out.Attr)
	child := &sftpNode{root: n.root, relPath: rel}
	stable := fs.StableAttr{Mode: out.Attr.Mode & syscall.S_IFMT}
	return n.NewInode(ctx, child, stable), &sftpHandle{f: f}, 0, 0
}

func (n *sftpNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	rel := n.childRelPath(name)
	abs := path.Join(n.root.remoteRoot, rel)
	if err := n.root.cli.Mkdir(abs); err != nil {
		return nil, sftpToErrno(err)
	}
	if mode != 0 {
		_ = n.root.cli.Chmod(abs, os.FileMode(mode&0o7777)) //nolint:errcheck,gosec
	}
	fi, err := n.root.cli.Lstat(abs)
	if err != nil {
		return nil, sftpToErrno(err)
	}
	fillAttrFromFileInfo(fi, &out.Attr)
	child := &sftpNode{root: n.root, relPath: rel}
	stable := fs.StableAttr{Mode: out.Attr.Mode & syscall.S_IFMT}
	return n.NewInode(ctx, child, stable), 0
}

func (n *sftpNode) Unlink(_ context.Context, name string) syscall.Errno {
	if err := n.root.cli.Remove(path.Join(n.remotePath(), name)); err != nil {
		return sftpToErrno(err)
	}
	return 0
}

func (n *sftpNode) Rmdir(_ context.Context, name string) syscall.Errno {
	if err := n.root.cli.RemoveDirectory(path.Join(n.remotePath(), name)); err != nil {
		return sftpToErrno(err)
	}
	return 0
}

func (n *sftpNode) Rename(_ context.Context, name string, newParent fs.InodeEmbedder, newName string, _ uint32) syscall.Errno {
	np, ok := newParent.(*sftpNode)
	if !ok {
		return syscall.EXDEV
	}
	oldAbs := path.Join(n.remotePath(), name)
	newAbs := path.Join(np.remotePath(), newName)
	if err := n.root.cli.Rename(oldAbs, newAbs); err != nil {
		return sftpToErrno(err)
	}
	return 0
}

func (n *sftpNode) Readlink(_ context.Context) ([]byte, syscall.Errno) {
	target, err := n.root.cli.ReadLink(n.remotePath())
	if err != nil {
		return nil, sftpToErrno(err)
	}
	return []byte(target), 0
}

func (n *sftpNode) Setattr(_ context.Context, _ fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	abs := n.remotePath()
	if mode, ok := in.GetMode(); ok {
		if err := n.root.cli.Chmod(abs, os.FileMode(mode&0o7777)); err != nil {
			return sftpToErrno(err)
		}
	}
	if size, ok := in.GetSize(); ok {
		if err := n.root.cli.Truncate(abs, int64(size)); err != nil { //nolint:gosec // FUSE size is uint64; sftp.Truncate takes int64. File sizes above 2^63 don't occur in practice
			return sftpToErrno(err)
		}
	}
	// Mtime/Atime: sftp.Client.Chtimes wraps SETSTAT with both.
	mtime, mtimeOK := in.GetMTime()
	atime, atimeOK := in.GetATime()
	if mtimeOK || atimeOK {
		if !mtimeOK {
			mtime = time.Now()
		}
		if !atimeOK {
			atime = time.Now()
		}
		if err := n.root.cli.Chtimes(abs, atime, mtime); err != nil {
			return sftpToErrno(err)
		}
	}
	fi, err := n.root.cli.Lstat(abs)
	if err != nil {
		return sftpToErrno(err)
	}
	fillAttrFromFileInfo(fi, &out.Attr)
	return 0
}

// sftpHandle is the FileHandle backing one Open/Create. The
// underlying sftp.File is goroutine-safe for its own use; we add no
// extra locking.
type sftpHandle struct {
	f *sftp.File
}

var (
	_ fs.FileReader   = (*sftpHandle)(nil)
	_ fs.FileWriter   = (*sftpHandle)(nil)
	_ fs.FileReleaser = (*sftpHandle)(nil)
	_ fs.FileFlusher  = (*sftpHandle)(nil)
)

func (h *sftpHandle) Read(_ context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	n, err := h.f.ReadAt(dest, off)
	if err != nil && err != io.EOF {
		return nil, sftpToErrno(err)
	}
	return fuse.ReadResultData(dest[:n]), 0
}

func (h *sftpHandle) Write(_ context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	n, err := h.f.WriteAt(data, off)
	if err != nil {
		return uint32(n), sftpToErrno(err) //nolint:gosec // WriteAt returns int <= len(data), FUSE limits per-write to max_write (typically <= 128 KiB)
	}
	return uint32(n), 0 //nolint:gosec // same: int n bounded by FUSE max_write
}

func (h *sftpHandle) Release(_ context.Context) syscall.Errno {
	if err := h.f.Close(); err != nil {
		return sftpToErrno(err)
	}
	return 0
}

func (h *sftpHandle) Flush(_ context.Context) syscall.Errno {
	// sftp.File has no separate flush; data is committed on each
	// WriteAt. Returning 0 is correct.
	return 0
}

// sftpToErrno maps an sftp.Client error to the closest syscall.Errno
// the kernel expects. The sftp library wraps the SSH-FX status codes
// into *sftp.StatusError; we keep the mapping narrow and fall back
// to EIO for the unfamiliar cases.
func sftpToErrno(err error) syscall.Errno {
	if err == nil {
		return 0
	}
	if errno, ok := err.(syscall.Errno); ok {
		return errno
	}
	if os.IsNotExist(err) {
		return syscall.ENOENT
	}
	if os.IsPermission(err) {
		return syscall.EACCES
	}
	if os.IsExist(err) {
		return syscall.EEXIST
	}
	// sftp.ErrSshFxNoSuchFile / similar are unwrapped via os.Is* above
	// in current pkg/sftp; everything else surfaces as EIO so the
	// shell tooling shows a clear "I/O error" rather than masking the
	// failure as ENOENT.
	return syscall.EIO
}
