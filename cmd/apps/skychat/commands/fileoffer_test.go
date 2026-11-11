package commands

import (
	"context"
	"net"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/dm"
	"github.com/skycoin/skywire/pkg/skychat/history"
	"github.com/skycoin/skywire/pkg/skychat/xfer"
)

type frameParker struct{ frames map[cipher.PubKey][]byte }

func (p *frameParker) Park(_ context.Context, pk cipher.PubKey, frame []byte, _ string) (cipher.PubKey, error) {
	p.frames[pk] = frame
	return cipher.PubKey{}, nil
}
func (p *frameParker) Unpark(context.Context) ([]dm.Parked, error) { return nil, nil }
func (p *frameParker) Ack(context.Context, []dm.Parked) error      { return nil }

// A file for a peer that cannot be dialed is offered on its inbox. The peer
// files the offer as a file it can request, once however often it arrives.
func TestSendFileID_ParkedWhenUnreachable(t *testing.T) {
	withXferEnv(t)
	restoreHistoryStore(t)
	historyStore = newTempStore(t, history.Limits{})
	withCleanDownloads(t)
	drain := collectEvents(t)

	box := &frameParker{frames: map[cipher.PubKey][]byte{}}
	origCtrl := chatCtrl
	chatCtrl = dm.New(dm.Config{Parker: box})
	t.Cleanup(func() { chatCtrl = origCtrl })
	fileMgrMu.Lock()
	origMgr := fileMgr
	fileMgr = xfer.NewManager(xfer.Config{Dial: func(context.Context, cipher.PubKey, uint16) (net.Conn, error) {
		return nil, net.ErrClosed
	}})
	fileMgrMu.Unlock()
	t.Cleanup(func() { fileMgrMu.Lock(); fileMgr = origMgr; fileMgrMu.Unlock() })

	peer, _ := cipher.GenerateKeyPair()
	id, err := sendFileID(context.Background(), peer, "", writeTempFile(t, "notes.txt", "hello"), "f1", "notes.txt", true)
	if err != errFileParked || id != "f1" {
		t.Fatalf("send = %q, %v; want f1 parked", id, err)
	}
	if m := storedFile(peer, "f1"); m == nil || m.FileStatus != fileStatusParked || !m.Outgoing {
		t.Fatalf("sender record = %+v", m)
	}
	frame := box.frames[peer]
	if frame == nil {
		t.Fatal("nothing parked")
	}

	// The peer's side.
	historyStore = newTempStore(t, history.Limits{})
	sender, _ := cipher.GenerateKeyPair()
	for range 2 {
		if !handleFileOfferFrame(sender, frame) {
			t.Fatal("offer not consumed")
		}
	}
	msgs, err := historyStore.ListByPeer(sender.Hex(), 0)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("peer history = %d (%v), want 1", len(msgs), err)
	}
	if m := msgs[0]; m.FileID != "f1" || m.FileStatus != fileStatusOffered || m.From != sender.Hex() || m.FileURL != "" {
		t.Errorf("offer = %+v", m)
	}
	var sawOffer bool
	for _, e := range drain() {
		sawOffer = sawOffer || (e.Dir == "in" && e.FileID == "f1" && e.FileStatus == fileStatusOffered)
	}
	if !sawOffer {
		t.Error("no offered event")
	}

	refileOffered(sender, "f1", "/files/f1.txt")
	if m := storedFile(sender, "f1"); m == nil || m.FileStatus != "received" || m.FileURL != "/files/f1.txt" {
		t.Errorf("after the bytes arrived = %+v", m)
	}
	if handleFileOfferFrame(sender, []byte("plain text")) {
		t.Error("plain text taken as an offer")
	}
}
