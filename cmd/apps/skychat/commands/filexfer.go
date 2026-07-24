// Package commands cmd/apps/skychat/commands/filexfer.go c4-app-chat
//
// File sharing for skychat (Telegram-style: a file is a message in the
// conversation). The bytes ride pkg/skychat/xfer over a skywire stream (skynet
// or dmsg — same port on both, skyenv.SkychatFilePort), OUT OF BAND from the
// chat-message conn so a large transfer never blocks chat. The offer + result
// surface as a chatEvent (file_* fields) into the same SSE/history stream as
// text messages, so a UI renders it inline like any other message.
//
// Auto-accept policy (operator decision): once you've ESTABLISHED a chat with a
// peer (there's a live conn) or you share a GROUP, inbound files are accepted
// automatically — no per-file prompt. Files from a peer you've never talked to
// are declined. Everything is encrypted by the skywire transport; nothing
// touches the clearnet.
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/imaging"

	"github.com/skycoin/skywire/cmd/apps/skychat/history"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skychat/xfer"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// fileMgr is the process-wide file-transfer manager, initialized by startFileXfer.
var (
	fileMgr   *xfer.Manager
	fileMgrMu sync.Mutex
)

// downloadsDir returns (and creates) the directory received files are saved to:
// <work-dir>/skychat-downloads, matching where the history DB lives.
func downloadsDir() (string, error) {
	workDir := ""
	if appCl != nil {
		workDir = appCl.Config().ProcWorkDir
	}
	if workDir == "" {
		workDir = skyenv.LocalPath
	}
	dir := filepath.Join(workDir, "skychat-downloads")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return dir, nil
}

// isEstablishedPeer reports whether we've already established a chat with pk (a
// live conn exists) — the "already established a chat" half of the auto-accept
// policy. Group membership is handled by the group path (filexfer_group.go).
func isEstablishedPeer(pk cipher.PubKey) bool {
	connsMu.Lock()
	_, ok := conns[pk]
	connsMu.Unlock()
	return ok
}

// fileDialFunc opens a fresh transfer stream to peer on the file port, trying
// skynet first (on-mesh, IP-anonymous) then dmsg — the same preference order as
// the chat conn. It always uses SkychatFilePort on both networks.
func fileDialFunc(_ context.Context, peer cipher.PubKey, port uint16) (net.Conn, error) {
	if appCl == nil {
		return nil, fmt.Errorf("skychat: app client not initialized")
	}
	var lastErr error
	for _, netType := range []appnet.Type{appnet.TypeSkynet, appnet.TypeDmsg} {
		addr := appnet.Addr{Net: netType, PubKey: peer, Port: routing.Port(port)}
		conn, err := appCl.Dial(addr)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("skychat: no network available for file dial")
	}
	return nil, lastErr
}

// acceptInbound implements the auto-accept policy and picks the save path. It
// returns a file handle under the downloads dir plus whether to accept.
func acceptInbound(from cipher.PubKey, offer xfer.Offer) (io.WriteCloser, bool) {
	// Group files: accepted when we share the offered group. 1:1 files: accepted
	// when a chat is already established with the sender.
	accept := false
	switch {
	case offer.Group != "":
		accept = isGroupMember(offer.Group, from)
	default:
		accept = isEstablishedPeer(from)
	}
	// A file we explicitly requested (backfill) is accepted even from a peer we
	// haven't otherwise established a chat with — we asked for it.
	if !accept && isRequestedFile(offer.ID, from) {
		accept = true
	}
	if !accept {
		appLog("skychat: declining file %q from unestablished peer %s", offer.Name, from)
		return nil, false
	}
	dir, err := downloadsDir()
	if err != nil {
		appLog("skychat: downloads dir: %v", err)
		return nil, false
	}
	dst := filepath.Join(dir, safeFileName(offer.Name, offer.ID))
	f, err := os.Create(dst) //nolint:gosec // dst is sanitized by safeFileName + rooted in downloadsDir
	if err != nil {
		appLog("skychat: create download %q: %v", dst, err)
		return nil, false
	}
	return &namedFile{File: f, path: dst}, true
}

// namedFile carries the save path alongside the handle so onDone can report it.
type namedFile struct {
	*os.File
	path string
}

// safeFileName reduces an offered name to a base name with no path separators or
// traversal, falling back to the transfer id when the name is empty or unsafe.
// It strips BOTH separators regardless of host OS so a Windows-style name from a
// peer ("..\\..\\evil.exe") is sanitized on a Linux host and vice-versa.
func safeFileName(name, id string) string {
	// Normalize both separators, then keep only the last path component.
	name = strings.ReplaceAll(name, `\`, "/")
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimLeft(name, ".")
	if name == "" || strings.Contains(name, "..") {
		name = "file-" + id
	}
	return name
}

// sentCopyName is the collision-free on-disk name for the sender's kept copy
// of an outbound file: the transfer's unique id plus the original extension
// (e.g. "a1b2c3d4e5f60718.png"). The original name is preserved separately for
// display (chatEvent/history FileName) and for the download filename; the
// id-based name only exists so a sent file never clobbers a same-named
// received file or another send. Always safeFileName-idempotent (a hex id + a
// separator-free extension), so /files/ and /thumb/ resolve it unchanged.
func sentCopyName(offer xfer.Offer) string {
	return offer.ID + filepath.Ext(offer.Name)
}

// copyFile copies src to dst (created/truncated).
func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // UI/operator-supplied local source
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }() //nolint:errcheck
	out, err := os.Create(dst)        //nolint:gosec // dst rooted in downloadsDir
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close() //nolint:errcheck
		return err
	}
	return out.Close()
}

// saveSentCopy keeps a served copy of an outbound file under the downloads dir
// (named by sentCopyName) so the sender's own UI and /history can render a
// thumbnail — the receiver already saves its own copy. DM sends only; the
// caller skips groups.
func saveSentCopy(srcPath string, offer xfer.Offer) error {
	dir, err := downloadsDir()
	if err != nil {
		return err
	}
	return copyFile(srcPath, filepath.Join(dir, sentCopyName(offer)))
}

// onXferDone surfaces a completed transfer (either direction) as a chatEvent and
// logs the result. It runs on the xfer manager's goroutine.
func onXferDone(dir xfer.Direction, peer cipher.PubKey, offer xfer.Offer, err error) {
	ev := chatEvent{
		ID:       offer.ID,
		Channel:  channelDM,
		FileID:   offer.ID,
		FileName: offer.Name,
		FileSize: offer.Size,
	}
	if offer.Group != "" {
		ev.Channel = channelGroup
		ev.GroupID = offer.Group
	}
	var wasRequested bool
	switch dir {
	case xfer.Incoming:
		ev.Dir = "in"
		ev.From = peer.Hex()
		wasRequested = isRequestedFile(offer.ID, peer)
		clearRequestedFile(offer.ID) // fulfilled (or terminal) — drop the pending request
		if err != nil {
			ev.FileStatus = "failed"
			ev.Text = fmt.Sprintf("📎 %s — receive failed: %v", offer.Name, err)
		} else {
			ev.FileStatus = "received"
			ev.Text = fmt.Sprintf("📎 %s (%s)", offer.Name, humanSize(offer.Size))
			if dir, derr := downloadsDir(); derr == nil {
				ev.FilePath = filepath.Join(dir, safeFileName(offer.Name, offer.ID))
			}
		}
	case xfer.Outgoing:
		ev.Dir = "out"
		ev.To = peer.Hex()
		if err != nil {
			ev.FileStatus = "failed"
			ev.Text = fmt.Sprintf("📎 %s — send failed: %v", offer.Name, err)
		} else {
			ev.FileStatus = "sent"
			ev.Text = fmt.Sprintf("📎 %s (%s)", offer.Name, humanSize(offer.Size))
			// Point at the sender's kept served copy (DM only) so the "out"
			// event carries a file_url and the sender's own thumbnail renders.
			if offer.Group == "" {
				if d, derr := downloadsDir(); derr == nil {
					ev.FilePath = filepath.Join(d, sentCopyName(offer))
				}
			}
		}
	}
	// A backfill response (a file we explicitly requested) patches the existing
	// referenced message rather than creating a new one — emit a file-ready
	// event carrying the file id + its now-servable URL. Everything else
	// surfaces as a normal file event.
	switch {
	case wasRequested && err == nil:
		if hub != nil {
			fileURL := ""
			if ev.FilePath != "" {
				fileURL = "/files/" + filepath.Base(ev.FilePath)
			}
			if body, mErr := json.Marshal(map[string]any{
				"channel":  "file-ready",
				"file_id":  offer.ID,
				"file_url": fileURL,
			}); mErr == nil {
				hub.broadcast(string(body))
			}
		}
	case hub != nil:
		hub.publishEvent(ev)
	}

	// Persist normal DM file events so they survive a cache wipe and reappear on
	// a fresh browser via /history (group files live in the group bucket, and
	// requested backfill responses patch an existing message — both skipped).
	// Best-effort — a persistence failure never blocks delivery.
	if offer.Group == "" && !wasRequested {
		msg := history.Message{
			Peer:       peer.Hex(),
			Outgoing:   dir == xfer.Outgoing,
			Text:       ev.Text,
			Timestamp:  time.Now().UTC(),
			FileName:   offer.Name,
			FileSize:   offer.Size,
			FileStatus: ev.FileStatus,
		}
		if dir == xfer.Incoming {
			msg.From = peer.Hex()
		}
		// Both directions now keep a served copy, so a set FilePath yields the
		// /files/ URL history uses to re-render the thumbnail on any device.
		if ev.FilePath != "" {
			msg.FileURL = "/files/" + filepath.Base(ev.FilePath)
		}
		persistMessage(msg)
	}

	if err != nil {
		appLog("skychat: file %s %s <-> %s: %v", dir, offer.Name, peer, err)
	} else {
		appLog("skychat: file %s %s <-> %s (%s)", dir, offer.Name, peer, humanSize(offer.Size))
	}
}

// startFileXfer builds the file-transfer manager and starts listeners on the
// file port over the enabled networks. Safe to call once during startup; a nil
// appCl (standalone/no-net) disables it.
func startFileXfer(ctx context.Context) {
	if appCl == nil {
		return
	}
	fileMgrMu.Lock()
	defer fileMgrMu.Unlock()
	if fileMgr != nil {
		return
	}
	mgr := xfer.NewManager(xfer.Config{
		LocalPK: appCl.Config().VisorPK,
		Dial:    fileDialFunc,
		Port:    skyenv.SkychatFilePort,
		Accept:  acceptInbound,
		OnDone:  onXferDone,
	})
	fileMgr = mgr

	var listeners []net.Listener
	for _, netType := range enabledFileNets() {
		l, err := appCl.Listen(netType, routing.Port(skyenv.SkychatFilePort))
		if err != nil {
			appLog("skychat: file listen on %s failed: %v", netType, err)
			continue
		}
		appLog("skychat: file transfer listening on %s port %d", netType, skyenv.SkychatFilePort)
		listeners = append(listeners, l)
	}
	if len(listeners) == 0 {
		appLog("skychat: file transfer has no listeners (no network enabled)")
		return
	}
	go mgr.Serve(ctx, listeners...)
}

// enabledFileNets returns the networks to listen on for file transfer, mirroring
// the chat listenLoop gating.
func enabledFileNets() []appnet.Type {
	var nets []appnet.Type
	if useSkynet {
		nets = append(nets, appnet.TypeSkynet)
	}
	if useDmsg {
		nets = append(nets, appnet.TypeDmsg)
	}
	return nets
}

// sendFileToPeer streams the file at path to peer (1:1). It runs the transfer to
// completion (blocking) and returns the transfer id and the receiver's verdict.
func sendFileToPeer(ctx context.Context, peer cipher.PubKey, path string) (string, error) {
	return sendFile(ctx, peer, "", path)
}

// sendFile streams path to peer; when group != "" the offer is stamped with the
// group id (used by the group path). Shared by 1:1 and group sends. Allocates a
// fresh transfer id, uses the file's base name for display, and keeps a served
// sender copy.
func sendFile(ctx context.Context, peer cipher.PubKey, group, path string) (string, error) {
	return sendFileID(ctx, peer, group, path, newEventID(), filepath.Base(path), true)
}

// sendFileID is sendFile with an explicit transfer id + display name and
// control over whether a served sender copy is kept. The re-send (backfill)
// path passes the ORIGINAL id + name — so the requester correlates the file to
// the referenced message and sees the real filename — and keepCopy=false (the
// file is already served locally; re-copying could truncate a same-path source).
func sendFileID(ctx context.Context, peer cipher.PubKey, group, path, id, name string, keepCopy bool) (string, error) {
	fileMgrMu.Lock()
	mgr := fileMgr
	fileMgrMu.Unlock()
	if mgr == nil {
		return "", fmt.Errorf("skychat: file transfer not enabled")
	}
	f, err := os.Open(path) //nolint:gosec // operator-supplied local path to send
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }() //nolint:errcheck
	fi, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("%s is a directory", path)
	}
	if name == "" {
		name = filepath.Base(path)
	}
	offer := xfer.Offer{
		ID:    id,
		Name:  name,
		Size:  fi.Size(),
		MIME:  mime.TypeByExtension(filepath.Ext(name)),
		Group: group,
	}
	// Keep a served copy of DM sends so the sender's own view + /history can
	// render a thumbnail (best-effort; a failure just means no sender-side
	// URL). Done before SendFile so the copy exists when onXferDone fires.
	if keepCopy && group == "" {
		if cErr := saveSentCopy(path, offer); cErr != nil {
			appLog("skychat: sender copy of %q failed: %v", name, cErr)
		}
	}
	rc, err := mgr.SendFile(ctx, peer, offer, f)
	if err != nil {
		return offer.ID, err
	}
	if !rc.OK {
		return offer.ID, fmt.Errorf("peer rejected transfer: %s", rc.Err)
	}
	return offer.ID, nil
}

// isGroupMember reports whether pk is a current member of the active CXO group
// groupID. Used by the auto-accept policy: a file stamped with a group we're in,
// coming from a fellow member, is accepted without a prompt.
func isGroupMember(groupID string, pk cipher.PubKey) bool {
	if cxoGroupSess == nil || groupID == "" || groupID != cxoGroup {
		return false
	}
	for _, p := range cxoGroupSess.PeerPKs() {
		if p == pk {
			return true
		}
	}
	return false
}

// sendFileToGroup fans the file at path out to every current member of the
// active CXO group over an encrypted file stream (members auto-accept). It
// returns the number of members the send was dispatched to; per-member results
// surface as individual "out" chatEvents via onXferDone. A member that's offline
// simply fails its leg — the others are unaffected.
func sendFileToGroup(ctx context.Context, path string) (int, error) {
	if cxoGroupSess == nil || cxoGroup == "" {
		return 0, fmt.Errorf("skychat: no active group")
	}
	peers := cxoGroupSess.PeerPKs()
	if len(peers) == 0 {
		return 0, fmt.Errorf("skychat: group %q has no other members online", cxoGroup)
	}
	for _, pk := range peers {
		pk := pk
		go func() {
			sctx, cancel := context.WithTimeout(ctx, sendTimeout)
			defer cancel()
			if _, err := sendFile(sctx, pk, cxoGroup, path); err != nil {
				appLog("skychat: group file to %s failed: %v", pk, err)
			}
		}()
	}
	return len(peers), nil
}

// humanSize renders a byte count as a short human string.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// sendTimeout bounds a single outbound file transfer.
const sendTimeout = 30 * time.Minute

// maxUploadMemory is the in-memory portion of a multipart upload before spilling
// to a temp file.
const maxUploadMemory = 8 << 20 // 8 MiB

// sendFileHandler serves POST /send-file. It sends a file either 1:1 (form field
// "pk"=recipient hex) or to the active group (form field "group"=group id or
// "1"). The file bytes come from a multipart "file" upload (browser) or, when no
// upload is present, a local "path" on the host (CLI). Responds with JSON
// {"id":...} on a dispatched send.
func sendFileHandler(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		// A multipart upload spills large bodies to temp files internally.
		if err := r.ParseMultipartForm(maxUploadMemory); err != nil && r.MultipartForm == nil { //nolint
			// Not multipart — fall back to plain form values (path-based send).
			if perr := r.ParseForm(); perr != nil { //nolint
				http.Error(w, "bad form: "+perr.Error(), http.StatusBadRequest)
				return
			}
		}

		pkHex := strings.TrimSpace(r.FormValue("pk"))       //nolint
		groupVal := strings.TrimSpace(r.FormValue("group")) //nolint

		// Resolve the source file: uploaded bytes take priority over a host path.
		path, cleanup, name, err := resolveUploadOrPath(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if cleanup != nil {
			defer cleanup()
		}
		_ = name // name is derived from path inside sendFile

		sctx, cancel := context.WithTimeout(ctx, sendTimeout)
		defer func() {
			// The group path returns immediately (fan-out goroutines own their own
			// ctx); the 1:1 path completes synchronously, so cancel after it.
			if pkHex != "" {
				cancel()
			}
		}()

		switch {
		case groupVal != "":
			// Visor-managed group (browser /group): publish a file reference on
			// the feed; members pull the bytes on demand via file-backfill. Falls
			// back to the standalone --cxo-group fan-out when pairing is off.
			if pairEnable && pairRPCAlive() {
				id, url, serr := sendFileToVisorGroup(ctx, groupVal, path, name)
				cancel()
				if serr != nil {
					http.Error(w, serr.Error(), http.StatusBadGateway)
					return
				}
				writeJSON(w, map[string]any{"id": id, "file_url": url, "group": groupVal})
				return
			}
			n, gerr := sendFileToGroup(ctx, path)
			if gerr != nil {
				cancel()
				http.Error(w, gerr.Error(), http.StatusServiceUnavailable)
				return
			}
			cancel()
			writeJSON(w, map[string]any{"dispatched": n, "group": cxoGroup})
		case pkHex != "":
			var pk cipher.PubKey
			if perr := pk.Set(pkHex); perr != nil {
				http.Error(w, "bad pk: "+perr.Error(), http.StatusBadRequest)
				return
			}
			id, serr := sendFileToPeer(sctx, pk, path)
			if serr != nil {
				http.Error(w, serr.Error(), http.StatusBadGateway)
				return
			}
			writeJSON(w, map[string]any{"id": id, "pk": pkHex})
		default:
			http.Error(w, "specify pk (1:1) or group", http.StatusBadRequest)
		}
	}
}

// resolveUploadOrPath returns a local filesystem path to send. If the request
// carries a multipart "file", it's saved to a temp file (returned with a cleanup
// func). Otherwise the "path" form value is used directly (no cleanup).
func resolveUploadOrPath(r *http.Request) (path string, cleanup func(), name string, err error) {
	if r.MultipartForm != nil {
		if f, hdr, ferr := r.FormFile("file"); ferr == nil {
			defer func() { _ = f.Close() }() //nolint:errcheck
			tmp, terr := os.CreateTemp("", "skychat-upload-*")
			if terr != nil {
				return "", nil, "", fmt.Errorf("temp file: %w", terr)
			}
			if _, cerr := io.Copy(tmp, f); cerr != nil {
				_ = tmp.Close()           //nolint:errcheck
				_ = os.Remove(tmp.Name()) //nolint:errcheck
				return "", nil, "", fmt.Errorf("save upload: %w", cerr)
			}
			_ = tmp.Close() //nolint:errcheck
			tmpPath := tmp.Name()
			// Preserve the original name for the offer by renaming into place.
			named := filepath.Join(filepath.Dir(tmpPath), safeFileName(hdr.Filename, filepath.Base(tmpPath)))
			if rerr := os.Rename(tmpPath, named); rerr == nil { //nolint
				tmpPath = named
			}
			cleanupPath := tmpPath
			return tmpPath, func() { _ = os.Remove(cleanupPath) }, hdr.Filename, nil //nolint:errcheck,gosec
		}
	}
	p := strings.TrimSpace(r.FormValue("path"))
	if p == "" {
		return "", nil, "", fmt.Errorf("no file: attach a multipart 'file' or set 'path'")
	}
	return p, nil, filepath.Base(p), nil
}

// mediaContentType maps a media file extension to its MIME type, or "" for
// anything not a recognized audio/video type. Used to set an explicit
// Content-Type before serving so the UI's <video>/<audio> elements play
// reliably — http.ServeFile otherwise depends on the host's mime.types,
// which is inconsistent across OSes and often missing webm/ogg/m4a/etc.
func mediaContentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".ogv":
		return "video/ogg"
	case ".mov":
		return "video/quicktime"
	case ".mkv":
		return "video/x-matroska"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg", ".oga":
		return "audio/ogg"
	case ".opus":
		return "audio/opus"
	case ".wav":
		return "audio/wav"
	case ".m4a", ".aac":
		return "audio/mp4"
	case ".flac":
		return "audio/flac"
	case ".weba":
		return "audio/webm"
	}
	return ""
}

// downloadFileHandler serves GET /files/<name> from the downloads dir so a UI can
// fetch a received file. The name is sanitized to a base name rooted in the
// downloads dir (no traversal). http.ServeFile honors HTTP Range requests, so
// <video>/<audio> streaming + seeking work without extra handling.
func downloadFileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/files/")
	name = safeFileName(name, "")
	if name == "" {
		http.Error(w, "no file", http.StatusBadRequest)
		return
	}
	dir, err := downloadsDir()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Set an explicit media Content-Type when we recognize the extension;
	// http.ServeFile keeps a Content-Type already on the header rather than
	// re-sniffing, so this wins for audio/video the host's mime table misses.
	if ct := mediaContentType(name); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	// name is a sanitized base name (safeFileName strips separators + traversal),
	// so the join stays rooted in the downloads dir.
	http.ServeFile(w, r, filepath.Join(dir, name)) //nolint:gosec // name sanitized by safeFileName
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// thumbMaxDim bounds the longest side of a generated thumbnail. 640px is
// crisp for an in-chat preview on hi-dpi displays while keeping the JPEG
// small (tens of KB) — so the browser fetches a lightweight thumbnail
// instead of decoding the full-resolution original for every preview.
const thumbMaxDim = 640

// makeThumbnail decodes the image at srcPath and returns a copy scaled to
// fit within maxDim×maxDim (aspect preserved). Returns an error for a
// missing file or an undecodable/non-image payload — the caller surfaces
// that so the UI can fall back to the full file.
func makeThumbnail(srcPath string, maxDim int) (image.Image, error) { //nolint:unparam
	img, err := imaging.Open(srcPath)
	if err != nil {
		return nil, err
	}
	return imaging.Fit(img, maxDim, maxDim, imaging.Lanczos), nil
}

// thumbnailHandler serves GET /thumb/<name>: a downscaled JPEG of a received
// image from the downloads dir. On any decode failure (a non-image file, or
// a format the decoder can't handle) it returns 415 so the UI's <img>
// onerror falls back to the full file at /files/<name>. The name is
// sanitized to a base name rooted in the downloads dir (no traversal), the
// same guard downloadFileHandler uses.
func thumbnailHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	name := safeFileName(strings.TrimPrefix(r.URL.Path, "/thumb/"), "")
	if name == "" {
		http.Error(w, "no file", http.StatusBadRequest)
		return
	}
	dir, err := downloadsDir()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// name is a sanitized base name, so the join stays inside downloadsDir.
	thumb, err := makeThumbnail(filepath.Join(dir, name), thumbMaxDim)
	if err != nil {
		http.Error(w, "not a decodable image", http.StatusUnsupportedMediaType)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if err := imaging.Encode(w, thumb, imaging.JPEG); err != nil {
		// Headers/body may be partially written by now; a log line is all
		// we can do (the client will see a truncated image and can retry).
		appLog("skychat: thumbnail encode %q: %v", name, err)
	}
}
