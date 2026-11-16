// Package commands cmd/apps/skychat/commands/filebackfill.go c4-app-chat
//
// File backfill: let a member who is missing a file's bytes ask the holder to
// re-send it. Files ride an out-of-band xfer stream at send time, so they are
// NOT replicated the way messages are (message history reaches a new
// group joiner via CXO feed replication; file bytes don't). This closes that
// gap with a small request/re-send protocol:
//
//  1. Requester POSTs /request-file {pk, file_id, file_name}. The app sends a
//     file-request control envelope to the peer (the file's original sender)
//     over the chat conn and records the id as expected.
//  2. Holder's handleConn recognizes the envelope, finds the file by id (the
//     sender's served copy is named <id><ext>, so it's directly lookup-able;
//     a received copy is checked too), and re-sends it over xfer preserving the
//     ORIGINAL id + name.
//  3. Requester's auto-accept admits the re-sent file because it asked for it,
//     even from a peer it hasn't otherwise established a chat with.
//
// The protocol is peer-to-peer by PK, so it serves DM file recovery directly
// and routes group-file backfill to the message's sender PK.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// fileReqType is the control-envelope type for a re-send request.
const fileReqType = "file-request"

// fileReqMsg is the JSON control envelope carried in a chat frame. Shape mirrors
// the pair-control envelopes so handleConn's fast-reject (non-'{' first byte)
// skips plain-text messages cheaply.
type fileReqMsg struct {
	Type   string `json:"type"`
	FileID string `json:"file_id"`
	Name   string `json:"file_name,omitempty"`
}

// requestedFiles tracks file ids this visor has asked a peer to (re)send, keyed
// by file id → the peer we expect the response from. Lets the auto-accept
// policy admit a requested file even from an unestablished peer.
var (
	requestedFilesMu sync.Mutex
	requestedFiles   = map[string]cipher.PubKey{}
)

func markFileRequested(fileID string, from cipher.PubKey) {
	requestedFilesMu.Lock()
	requestedFiles[fileID] = from
	requestedFilesMu.Unlock()
}

// isRequestedFile reports whether (fileID, from) matches an outstanding
// request — i.e. we asked `from` to send us this file.
func isRequestedFile(fileID string, from cipher.PubKey) bool {
	requestedFilesMu.Lock()
	defer requestedFilesMu.Unlock()
	want, ok := requestedFiles[fileID]
	return ok && want == from
}

func clearRequestedFile(fileID string) {
	requestedFilesMu.Lock()
	delete(requestedFiles, fileID)
	requestedFilesMu.Unlock()
}

// findFileByID locates a locally-held copy of a transfer by its id, checking
// both the sender-copy naming (<id><ext>) and the received-file naming
// (safeFileName(name, id)). Returns the path when a regular file is found.
func findFileByID(fileID, fileName string) (string, bool) {
	dir, err := downloadsDir()
	if err != nil {
		return "", false
	}
	candidates := []string{
		filepath.Join(dir, fileID+strings.ToLower(filepath.Ext(fileName))),
		filepath.Join(dir, safeFileName(fileName, fileID)),
	}
	for _, p := range candidates {
		if fi, statErr := os.Stat(p); statErr == nil && !fi.IsDir() {
			return p, true
		}
	}
	return "", false
}

// handleFileRequestFrame interprets a frame as a file-request control envelope.
// Returns true when the frame was a (well-formed) file-request and was consumed
// — so handleConn doesn't surface it as a chat message. When we hold the file,
// it is re-sent to the requester over xfer, preserving the original id + name.
func handleFileRequestFrame(ctx context.Context, requester cipher.PubKey, raw []byte) bool {
	trimmed := bytesTrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	var env fileReqMsg
	if err := json.Unmarshal(trimmed, &env); err != nil || env.Type != fileReqType || env.FileID == "" {
		return false
	}
	path, ok := findFileByID(env.FileID, env.Name)
	if !ok {
		appLog("skychat: file-request %s from %s — not held locally", env.FileID, requester)
		return true // consumed; we simply don't have it
	}
	appLog("skychat: re-sending file %s (%q) to %s on request", env.FileID, env.Name, requester)
	go func() {
		sctx, cancel := context.WithTimeout(ctx, sendTimeout)
		defer cancel()
		if _, err := sendFileID(sctx, requester, "", path, env.FileID, env.Name, false); err != nil {
			appLog("skychat: file-request re-send %s to %s failed: %v", env.FileID, requester, err)
		}
	}()
	return true
}

// requestFileHandler serves POST /request-file {pk, file_id, file_name}: send a
// file-request control envelope to a peer (the file's original sender) and mark
// the id as expected so the re-sent file is auto-accepted.
func requestFileHandler(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			PK       string `json:"pk"`
			FileID   string `json:"file_id"`
			FileName string `json:"file_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(body.FileID) == "" {
			http.Error(w, "file_id required", http.StatusBadRequest)
			return
		}
		peer, err := parsePK(body.PK)
		if err != nil {
			http.Error(w, "invalid pk: "+err.Error(), http.StatusBadRequest)
			return
		}
		if appCl == nil {
			http.Error(w, "standalone mode: no visor transport for file requests", http.StatusServiceUnavailable)
			return
		}
		if chatCtrl == nil {
			http.Error(w, "skychat: dm controller not started", http.StatusServiceUnavailable)
			return
		}
		if err := requestFileFrom(ctx, peer, body.FileID, body.FileName); err != nil {
			http.Error(w, "send request: "+err.Error(), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// requestFileFrom asks peer to re-send the file fileID. The id is marked as
// expected before the request goes out, so a fast answer is not taken for a
// new file.
func requestFileFrom(ctx context.Context, peer cipher.PubKey, fileID, name string) error {
	if chatCtrl == nil {
		return errors.New("skychat: dm controller not started")
	}
	payload, err := json.Marshal(fileReqMsg{Type: fileReqType, FileID: fileID, Name: name})
	if err != nil {
		return err
	}
	markFileRequested(fileID, peer)
	// Raw control frame: the peer consumes it in PreHandleFrame, so it is
	// never surfaced or persisted as a chat message.
	if err := chatCtrl.SendRaw(ctx, peer, payload); err != nil {
		clearRequestedFile(fileID)
		return err
	}
	return nil
}

// Group files are pulled on demand. A recorded voice or video message is
// pulled as it arrives, since it is useless as a bare reference.
const (
	clipPullMaxSize = 16 << 20
	clipPullMaxAge  = time.Hour
)

// clipPull requests a file; a variable so tests can watch it.
var clipPull = func(peer cipher.PubKey, fileID, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	if err := requestFileFrom(ctx, peer, fileID, name); err != nil {
		clearRequestedFile(fileID)
		appLog("skychat: pull of group clip %s from %s failed: %v", fileID, peer, err)
	}
}

// recordedClip reports whether name is a clip the composer recorded
// (voice-<stamp>, video-<stamp>, video-sq-<stamp>), the names every
// skychat client gives them.
func recordedClip(name string) bool {
	n := strings.ToLower(name)
	for _, p := range []string{"voice-", "video-sq-", "video-"} {
		if rest, ok := strings.CutPrefix(n, p); ok && rest != "" && rest[0] >= '0' && rest[0] <= '9' {
			return true
		}
	}
	return false
}

// pullGroupClip fetches the bytes of a recorded clip posted to a group by
// someone else, when they are not held here and the post is recent.
func pullGroupClip(sender cipher.PubKey, ts time.Time, meta groupFileMeta) {
	if !recordedClip(meta.Name) || meta.Size > clipPullMaxSize || time.Since(ts) > clipPullMaxAge {
		return
	}
	if appCl != nil && sender == appCl.Config().VisorPK {
		return
	}
	if _, held := findFileByID(meta.ID, meta.Name); held || isRequestedFile(meta.ID, sender) {
		return
	}
	markFileRequested(meta.ID, sender)
	go clipPull(sender, meta.ID, meta.Name)
}
