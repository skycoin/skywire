// Package history cmd/apps/skychat/history/file_message_test.go
//
// A persisted file message must round-trip its file_* metadata through the
// store so /history can re-render media (thumbnail/player) on a fresh
// browser after the localStorage cache is gone.
package history

import (
	"testing"
	"time"
)

func TestBoltStore_FileMessageRoundTrip(t *testing.T) {
	s := newTestStore(t, DefaultLimits())

	in := Message{
		Peer:       "peer-pk",
		From:       "peer-pk",
		Outgoing:   false,
		Text:       "📎 photo.png (2.0 KB)",
		Timestamp:  time.Now().UTC(),
		FileName:   "photo.png",
		FileSize:   2048,
		FileStatus: "received",
		FileURL:    "/files/photo.png",
	}
	if err := s.Append(in); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := s.ListByPeer("peer-pk", 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("ListByPeer: err=%v len=%d", err, len(got))
	}
	m := got[0]
	if m.FileName != "photo.png" || m.FileSize != 2048 || m.FileStatus != "received" || m.FileURL != "/files/photo.png" {
		t.Errorf("file metadata did not round-trip: %+v", m)
	}

	// A plain text message stays free of file metadata.
	if err := s.Append(Message{Peer: "peer-pk", Text: "hi", Timestamp: time.Now().UTC()}); err != nil {
		t.Fatalf("Append text: %v", err)
	}
	all, err := s.ListByPeer("peer-pk", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, mm := range all {
		if mm.Text == "hi" && (mm.FileName != "" || mm.FileURL != "") {
			t.Errorf("text message gained file metadata: %+v", mm)
		}
	}
}
