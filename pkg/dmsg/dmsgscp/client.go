// Package dmsgscp pkg/dmsg/dmsgscp/client.go: the dialing side of
// scp-over-dmsg. A Client wraps a single dmsg.Stream and exposes
// Download / Upload methods that drive the same wire framing the
// Host serves.
//
// A Client is single-use by design: each transfer establishes a
// fresh dmsg stream, runs the framing end-to-end, and closes. This
// keeps the failure model simple — half-finished transfers can't
// pollute a long-lived stream — and matches how the CLI invokes it.
package dmsgscp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// Client is a single-transfer dmsgscp client. Build one via Dial.
type Client struct {
	conn   *dmsg.Stream
	reader *bufio.Reader
	remote cipher.PubKey
}

// Dial opens a dmsg.Stream to (rPK:port) and returns a Client
// ready to drive Upload/Download. The caller must Close the
// Client when done — Upload/Download don't close on success so the
// caller can chain (in practice, every CLI path closes immediately).
func Dial(ctx context.Context, dmsgC *dmsg.Client, rPK cipher.PubKey, port uint16) (*Client, error) {
	if dmsgC == nil {
		return nil, errors.New("dmsgscp: nil dmsg client")
	}
	addr := dmsg.Addr{PK: rPK, Port: port}
	stream, err := dmsgC.DialStream(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("dmsgscp: dial %s:%d: %w", rPK, port, err)
	}
	return &Client{
		conn:   stream,
		reader: bufio.NewReader(stream),
		remote: rPK,
	}, nil
}

// Close releases the underlying stream.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// Download pulls a file from the remote host's rootDir at
// remotePath and writes it to localPath. Existing files at
// localPath are overwritten.
func (c *Client) Download(remotePath, localPath string) error {
	if c.conn == nil {
		return errors.New("dmsgscp: client closed")
	}

	// Step 1: send role line.
	if _, err := fmt.Fprintf(c.conn, "%s %s\n", RoleSource, remotePath); err != nil {
		return fmt.Errorf("dmsgscp: write role line: %w", err)
	}

	// Step 2: read the C-record from the host.
	hdr, err := ReadHeader(c.reader)
	if err != nil {
		return fmt.Errorf("dmsgscp: read header: %w", err)
	}
	if hdr.Type == RecordDirStart || hdr.Type == RecordDirEnd {
		return ErrDirNotSupported
	}
	if hdr.Type != RecordFile {
		return fmt.Errorf("dmsgscp: unexpected record type %c", hdr.Type)
	}

	// Step 3: ack the header.
	if err := WriteAck(c.conn); err != nil {
		return fmt.Errorf("dmsgscp: ack header: %w", err)
	}

	// Step 4: open destination, then read exactly Size bytes.
	// Mode comes from the wire, masked to 0700 (umask-style) — same
	// hardening as the host's SINK path.
	mode := hdr.Mode & 0700
	if mode == 0 {
		mode = 0600
	}
	// Ensure the parent directory exists so callers can drop into
	// an arbitrary path without pre-creating.
	if dir := filepath.Dir(localPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("dmsgscp: mkdir parent: %w", err)
		}
	}
	f, err := os.OpenFile(localPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode) //nolint:gosec
	if err != nil {
		return fmt.Errorf("dmsgscp: open local file: %w", err)
	}

	cleanup := func(success bool) error {
		closeErr := f.Close()
		if !success {
			if rerr := os.Remove(localPath); rerr != nil && !os.IsNotExist(rerr) {
				return fmt.Errorf("close=%v, remove=%w", closeErr, rerr)
			}
		}
		return closeErr
	}

	if _, err := io.CopyN(f, c.reader, hdr.Size); err != nil {
		_ = cleanup(false) //nolint:errcheck
		return fmt.Errorf("dmsgscp: read payload: %w", err)
	}

	// Step 5: read the host's trailing ack on the payload.
	if msg, err := ReadAck(c.reader); err != nil {
		_ = cleanup(false) //nolint:errcheck
		if len(msg) > 0 {
			return fmt.Errorf("dmsgscp: peer error: %s: %w", msg, err)
		}
		return fmt.Errorf("dmsgscp: read trailing ack: %w", err)
	}

	// Step 6: send our final ack.
	if err := WriteAck(c.conn); err != nil {
		_ = cleanup(false) //nolint:errcheck
		return fmt.Errorf("dmsgscp: final ack: %w", err)
	}

	return cleanup(true)
}

// Upload pushes localPath to the remote host at remotePath.
// remotePath is interpreted relative to the host's rootDir; the
// basename of remotePath becomes the C-record's name.
func (c *Client) Upload(localPath, remotePath string) error {
	if c.conn == nil {
		return errors.New("dmsgscp: client closed")
	}

	info, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("dmsgscp: stat local: %w", err)
	}
	if info.IsDir() {
		return errors.New("dmsgscp: directory upload not supported in v1")
	}
	if info.Size() > MaxFileSize {
		return fmt.Errorf("dmsgscp: local file exceeds %d-byte cap", MaxFileSize)
	}

	// Step 1: send role line.
	if _, err := fmt.Fprintf(c.conn, "%s %s\n", RoleSink, remotePath); err != nil {
		return fmt.Errorf("dmsgscp: write role line: %w", err)
	}

	// Step 2: wait for the host's ready ack.
	if msg, err := ReadAck(c.reader); err != nil {
		if len(msg) > 0 {
			return fmt.Errorf("dmsgscp: peer error: %s: %w", msg, err)
		}
		return fmt.Errorf("dmsgscp: ready ack: %w", err)
	}

	// Step 3: send C-record. Basename only — the destination
	// directory came from the role line.
	base := filepath.Base(remotePath)
	if err := WriteFileHeader(c.conn, info.Mode(), info.Size(), base); err != nil {
		return fmt.Errorf("dmsgscp: write header: %w", err)
	}

	// Step 4: wait for header ack.
	if msg, err := ReadAck(c.reader); err != nil {
		if len(msg) > 0 {
			return fmt.Errorf("dmsgscp: peer error: %s: %w", msg, err)
		}
		return fmt.Errorf("dmsgscp: header ack: %w", err)
	}

	// Step 5: stream the payload.
	f, err := os.Open(localPath) //nolint:gosec // path supplied by local caller
	if err != nil {
		return fmt.Errorf("dmsgscp: open local file: %w", err)
	}
	defer f.Close() //nolint:errcheck

	if _, err := io.CopyN(c.conn, f, info.Size()); err != nil {
		return fmt.Errorf("dmsgscp: write payload: %w", err)
	}

	// Step 6: send trailing ack.
	if err := WriteAck(c.conn); err != nil {
		return fmt.Errorf("dmsgscp: write trailing ack: %w", err)
	}

	// Step 7: wait for the host's final ack.
	if msg, err := ReadAck(c.reader); err != nil {
		if len(msg) > 0 {
			return fmt.Errorf("dmsgscp: peer error: %s: %w", msg, err)
		}
		return fmt.Errorf("dmsgscp: final ack: %w", err)
	}

	return nil
}
