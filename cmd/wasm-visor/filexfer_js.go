//go:build js && wasm

// Package main — in-process file transfer for the browser visor.
//
// This is the wasm counterpart of the native skychat app's file sharing
// (cmd/apps/skychat/filexfer.go). It reuses the SAME pkg/skychat/xfer primitive
// (offer → accept → chunked body → sha256 trailer → receipt) over the SAME
// app.Client the browser skychat already uses, on skyenv.SkychatFilePort — so a
// browser tab and a native visor exchange files wire-compatibly.
//
// The one browser-specific difference: a tab has no filesystem. A RECEIVED file
// is buffered in memory and handed to the page as base64 via skychatFile(id) so
// the page can trigger a download (a Blob). A SENT file's bytes come FROM the
// page (a File read to base64) into skychatSendFile(). Received files surface as
// chatMsg entries (is_file=true) in the same buffer as text messages, so the UI
// renders them inline like Telegram.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall/js"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skychat/xfer"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	fileMgr    *xfer.Manager
	fileMu     sync.Mutex
	pendingBuf = map[string]*bytes.Buffer{} // offerID → sink while a transfer is in flight
	recvFiles  = map[string][]byte{}        // offerID → verified received bytes (for download)
)

// memSink is an in-memory WriteCloser: a browser tab can't write to disk, so a
// received file streams into a buffer registered under its transfer id.
type memSink struct {
	buf *bytes.Buffer
}

func (m *memSink) Write(p []byte) (int, error) { return m.buf.Write(p) }
func (m *memSink) Close() error                { return nil }

// startFileXferWasm builds the file-transfer manager on the browser skychat's
// app.Client and listens on the file port over the same networks as chat.
func startFileXferWasm(ctx context.Context, cl *app.Client) {
	mgr := xfer.NewManager(xfer.Config{
		LocalPK: cl.Config().VisorPK,
		Dial:    fileDialWasm,
		Port:    skyenv.SkychatFilePort,
		Accept:  acceptFileWasm,
		OnDone:  onFileDoneWasm,
	})
	fileMu.Lock()
	fileMgr = mgr
	fileMu.Unlock()

	var listeners []net.Listener
	for _, n := range []appnet.Type{appnet.TypeDmsg, appnet.TypeSkynet} {
		lis, err := cl.Listen(n, routing.Port(skyenv.SkychatFilePort))
		if err != nil {
			vlog(fmt.Sprintf("skychat: file listen %s:%d: %s", n, skyenv.SkychatFilePort, err.Error()))
			continue
		}
		vlog(fmt.Sprintf("skychat: file transfer listening on %s:%d", n, skyenv.SkychatFilePort))
		go func(l net.Listener) { <-ctx.Done(); l.Close() }(lis) //nolint:errcheck,gosec
		listeners = append(listeners, lis)
	}
	if len(listeners) == 0 {
		vlog("skychat: file transfer has no listeners")
		return
	}
	go mgr.Serve(ctx, listeners...)
}

// fileDialWasm opens a fresh transfer stream to peer on the file port, dmsg-first
// (the browser's reliable path) then skynet, via the browser skychat app.Client.
func fileDialWasm(_ context.Context, peer cipher.PubKey, port uint16) (net.Conn, error) {
	if chatClient == nil {
		return nil, fmt.Errorf("skychat not started yet")
	}
	var lastErr error
	for _, n := range []appnet.Type{appnet.TypeDmsg, appnet.TypeSkynet} {
		conn, err := chatClient.Dial(appnet.Addr{Net: n, PubKey: peer, Port: routing.Port(port)})
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no network available")
	}
	return nil, lastErr
}

// acceptFileWasm auto-accepts inbound transfers (the tab is the user's own trust
// boundary — the "already established a chat / in a group" gate on the native
// side has no browser filesystem analogue; a stranger simply can't route to a
// tab without the user's PK) and streams into an in-memory buffer keyed by id.
func acceptFileWasm(_ cipher.PubKey, offer xfer.Offer) (io.WriteCloser, bool) {
	buf := &bytes.Buffer{}
	fileMu.Lock()
	pendingBuf[offer.ID] = buf
	fileMu.Unlock()
	return &memSink{buf: buf}, true
}

// onFileDoneWasm moves a verified received file into recvFiles for download and
// surfaces the transfer (either direction) as a chatMsg entry.
func onFileDoneWasm(dir xfer.Direction, peer cipher.PubKey, offer xfer.Offer, err error) {
	m := chatMsg{
		From:     peer.Hex(),
		TS:       nowMs(),
		IsFile:   true,
		FileID:   offer.ID,
		FileName: offer.Name,
		FileSize: offer.Size,
		FileOK:   err == nil,
	}
	switch dir {
	case xfer.Incoming:
		m.Out = false
		fileMu.Lock()
		if buf, ok := pendingBuf[offer.ID]; ok {
			if err == nil {
				recvFiles[offer.ID] = buf.Bytes()
			}
			delete(pendingBuf, offer.ID)
		}
		fileMu.Unlock()
		if err != nil {
			m.Text = fmt.Sprintf("📎 %s — receive failed: %v", offer.Name, err)
		} else {
			m.Text = fmt.Sprintf("📎 %s (%d bytes)", offer.Name, offer.Size)
		}
	case xfer.Outgoing:
		m.Out = true
		if err != nil {
			m.Text = fmt.Sprintf("📎 %s — send failed: %v", offer.Name, err)
		} else {
			m.Text = fmt.Sprintf("📎 %s (%d bytes)", offer.Name, offer.Size)
		}
	}
	appendChat(m)
	if err != nil {
		vlog(fmt.Sprintf("skychat: file %s %s: %s", dir, offer.Name, err.Error()))
	} else {
		vlog(fmt.Sprintf("skychat: file %s %s (%d bytes) with %s", dir, offer.Name, offer.Size, shortPK(peer.Hex())))
	}
}

// sendFileWasm streams data to peer as a file named name.
func sendFileWasm(pkHex, name string, data []byte) (string, error) {
	fileMu.Lock()
	mgr := fileMgr
	fileMu.Unlock()
	if mgr == nil {
		return "", fmt.Errorf("file transfer not started yet")
	}
	var pk cipher.PubKey
	if err := pk.Set(pkHex); err != nil {
		return "", fmt.Errorf("bad peer pk: %w", err)
	}
	offer := xfer.Offer{ID: newFileID(), Name: name, Size: int64(len(data))}
	vlog(fmt.Sprintf("skychat: sending file %q (%d bytes) to %s…", name, len(data), shortPK(pkHex)))
	rc, err := mgr.SendFile(context.Background(), pk, offer, bytes.NewReader(data))
	if err != nil {
		return offer.ID, err
	}
	if !rc.OK {
		return offer.ID, fmt.Errorf("peer rejected: %s", rc.Err)
	}
	return offer.ID, nil
}

// fileIDSeq disambiguates two transfers started within the same millisecond.
var fileIDSeq uint64

// newFileID is a short unique-within-the-tab-session transfer id (time + a
// monotonic counter, so same-millisecond sends don't collide).
func newFileID() string {
	fileMu.Lock()
	fileIDSeq++
	seq := fileIDSeq
	fileMu.Unlock()
	return fmt.Sprintf("f%d-%d", nowMs(), seq)
}

// jsSkychatSendFile(peerPkHex, fileName, base64Bytes) → Promise<{id}>.
// The page reads a File (FileReader.readAsDataURL / arrayBuffer → base64) and
// passes the bytes; they stream peer-to-peer over the encrypted transport.
func jsSkychatSendFile(_ js.Value, args []js.Value) interface{} {
	if len(args) < 3 {
		return js.Global().Get("Error").New("skychatSendFile(peerPkHex, fileName, base64Bytes)")
	}
	pkHex, name, b64 := args[0].String(), args[1].String(), args[2].String()
	return promise(func() (interface{}, error) {
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("bad base64: %w", err)
		}
		id, serr := sendFileWasm(pkHex, name, data)
		if serr != nil {
			return nil, serr
		}
		return map[string]interface{}{"id": id}, nil
	})
}

// jsSkychatFile(id) → base64 of a received file (or "" if unknown). The page
// base64-decodes it into a Blob and triggers a download.
func jsSkychatFile(_ js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return ""
	}
	id := args[0].String()
	fileMu.Lock()
	data, ok := recvFiles[id]
	fileMu.Unlock()
	if !ok {
		return ""
	}
	return base64.StdEncoding.EncodeToString(data)
}
