// Package commands cmd/apps/skychat/commands/fileoffer.go c4-app-chat
//
// A file sent to a peer that cannot be reached is offered on its dmsg-async
// inbox instead. The peer files the offer as a file message without bytes,
// and its "Re-request file" asks the sender for them by the transfer id, the
// same request a backfill makes (filebackfill.go).
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/history"
	"github.com/skycoin/skywire/pkg/skychat/xfer"
)

const fileOfferType = "file-offer"

// File status of an offer, on the sender and on the peer.
const (
	fileStatusParked  = "parked"
	fileStatusOffered = "offered"
)

// errFileParked is a send that did not go out and was offered instead.
var errFileParked = errors.New("skychat: peer unreachable, file offered on its inbox")

type fileOfferMsg struct {
	Type   string `json:"type"`
	FileID string `json:"file_id"`
	Name   string `json:"file_name"`
	Size   int64  `json:"file_size"`
	MIME   string `json:"mime,omitempty"`
}

// parkFileOffer offers the file to peer on its inbox and records the offer
// here. The sender's served copy is what a later request re-sends.
func parkFileOffer(ctx context.Context, peer cipher.PubKey, offer xfer.Offer) bool {
	if chatCtrl == nil {
		return false
	}
	payload, err := json.Marshal(fileOfferMsg{Type: fileOfferType, FileID: offer.ID, Name: offer.Name, Size: offer.Size, MIME: offer.MIME})
	if err != nil || !chatCtrl.ParkFrame(ctx, peer, payload, offer.ID) {
		return false
	}
	text := fmt.Sprintf("📎 %s (%s)", offer.Name, humanSize(offer.Size))
	msg := history.Message{
		Peer: peer.Hex(), Outgoing: true, Text: text, Timestamp: time.Now().UTC(), ID: offer.ID,
		FileID: offer.ID, FileName: offer.Name, FileSize: offer.Size, FileStatus: fileStatusParked,
	}
	ev := chatEvent{
		ID: offer.ID, Channel: channelDM, Dir: "out", To: peer.Hex(), Text: text,
		FileID: offer.ID, FileName: offer.Name, FileSize: offer.Size, FileStatus: fileStatusParked,
	}
	if d, derr := downloadsDir(); derr == nil {
		ev.FilePath = filepath.Join(d, sentCopyName(offer))
		msg.FileURL = "/files/" + filepath.Base(ev.FilePath)
	}
	persistMessage(msg)
	if hub != nil {
		hub.publishEvent(ev)
	}
	appLog("skychat: %s is unreachable; offered %q on its inbox", peer, offer.Name)
	return true
}

// handleFileOfferFrame files a file offer from peer as a file message without
// bytes. It reports whether raw was an offer.
func handleFileOfferFrame(peer cipher.PubKey, raw []byte) bool {
	trimmed := bytesTrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	var o fileOfferMsg
	if err := json.Unmarshal(trimmed, &o); err != nil || o.Type != fileOfferType || o.FileID == "" {
		return false
	}
	if storedFile(peer, o.FileID) != nil {
		return true
	}
	name := safeFileName(o.Name, o.FileID)
	text := fmt.Sprintf("📎 %s (%s)", name, humanSize(o.Size))
	persistMessage(history.Message{
		Peer: peer.Hex(), From: peer.Hex(), Text: text, Timestamp: time.Now().UTC(), ID: o.FileID,
		FileID: o.FileID, FileName: name, FileSize: o.Size, FileStatus: fileStatusOffered,
	})
	if hub != nil {
		hub.publishEvent(chatEvent{
			ID: o.FileID, Channel: channelDM, Dir: "in", From: peer.Hex(), Text: text,
			FileID: o.FileID, FileName: name, FileSize: o.Size, FileStatus: fileStatusOffered,
		})
	}
	notifyOSInboundThread(peer.Hex(), peer.Hex(), displayName(peer.Hex()),
		notifPreview("📎 "+fileKindLabel(name, o.MIME)+": "+name))
	return true
}

// storedFile returns peer's stored message for file id, if any.
func storedFile(peer cipher.PubKey, id string) *history.Message {
	if historyStore == nil {
		return nil
	}
	msgs, err := historyStore.ListByPeer(peer.Hex(), 0)
	if err != nil {
		return nil
	}
	for i := range msgs {
		if msgs[i].FileID == id {
			return &msgs[i]
		}
	}
	return nil
}

// refileOffered replaces a stored offer whose bytes have now arrived with the
// received file, at the offer's time, so the file survives a reload.
func refileOffered(peer cipher.PubKey, id, fileURL string) {
	m := storedFile(peer, id)
	if m == nil || m.FileStatus != fileStatusOffered {
		return
	}
	got := *m
	got.FileStatus, got.FileURL = "received", fileURL
	if _, err := historyStore.DeleteByID(peer.Hex(), id); err != nil {
		return
	}
	persistMessage(got)
}
