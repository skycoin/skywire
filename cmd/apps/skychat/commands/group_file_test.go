// Package commands cmd/apps/skychat/commands/group_file_test.go
//
// Unit coverage for the group file-share feature: the file-reference envelope
// that rides a group feed (encode/parse), the read-side enrichment that adds
// file_* fields (+ a /files/ URL when the bytes are held locally), the
// sender-side publish (served copy + GroupSend of the reference), and the
// end-to-end enrichment through the /group/<id>/history handler.
package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/visor"
)

func TestEncodeParseGroupFileText_RoundTrip(t *testing.T) {
	meta := groupFileMeta{ID: "gf-abc123", Name: "holiday photo.png", Size: 4096}
	text, err := encodeGroupFileText(meta)
	if err != nil {
		t.Fatalf("encodeGroupFileText: %v", err)
	}
	got, ok := parseGroupFileText(text)
	if !ok {
		t.Fatalf("parseGroupFileText did not recognize its own output: %q", text)
	}
	if got != meta {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, meta)
	}
}

func TestParseGroupFileText_Negatives(t *testing.T) {
	// Each of these must NOT be read as a file reference: the cheap prefix/
	// substring gate rejects the first three without touching JSON; the last
	// two reach the parser but fail the envelope/id checks.
	for _, txt := range []string{
		"",                                  // empty
		"just a normal chat line",           // no leading brace
		"{ not json at all",                 // brace but no marker
		`{"skychat_reply":{"text":"hi"}}`,   // different envelope (no marker substring)
		`{"skychat_file":{"name":"x.png"}}`, // marker present but id empty
	} {
		if _, ok := parseGroupFileText(txt); ok {
			t.Errorf("parseGroupFileText(%q) = true, want false", txt)
		}
	}
}

func TestEnrichGroupFileRow(t *testing.T) {
	meta := groupFileMeta{ID: "enrich-1", Name: "report.pdf", Size: 42}

	// Bytes not held locally → descriptive fields present, but no file_url.
	row := map[string]any{}
	enrichGroupFileRow(row, meta)
	if row["file_id"] != "enrich-1" || row["file_name"] != "report.pdf" {
		t.Errorf("file id/name not set: %v", row)
	}
	if sz, ok := row["file_size"].(int64); !ok || sz != 42 {
		t.Errorf("file_size = %v, want int64 42", row["file_size"])
	}
	if _, ok := row["file_url"]; ok {
		t.Error("file_url must be absent when the bytes are not held locally")
	}

	// Drop the served copy into the downloads dir (sender-copy naming <id><ext>)
	// so findFileByID resolves it and a /files/ URL is added.
	dir, err := downloadsDir()
	if err != nil {
		t.Fatalf("downloadsDir: %v", err)
	}
	served := filepath.Join(dir, "enrich-1.pdf")
	if err := os.WriteFile(served, []byte("pdf-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(served) }) //nolint

	row2 := map[string]any{}
	enrichGroupFileRow(row2, meta)
	if row2["file_url"] != "/files/enrich-1.pdf" {
		t.Errorf("file_url = %v, want /files/enrich-1.pdf", row2["file_url"])
	}
}

func TestSendFileToVisorGroup(t *testing.T) {
	fake := &groupAPI{}
	withFakePairRPC(t, fake)

	src := filepath.Join(t.TempDir(), "pic.png")
	if err := os.WriteFile(src, []byte("imgbytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	fileID, url, err := sendFileToVisorGroup(context.Background(), "gid-9", src, "pic.png")
	if err != nil {
		t.Fatalf("sendFileToVisorGroup: %v", err)
	}
	if fileID == "" {
		t.Fatal("empty file id")
	}
	// The optimistic sender URL points at the id-named served copy.
	if url != "/files/"+fileID+".png" {
		t.Errorf("url = %q, want /files/%s.png", url, fileID)
	}

	// A served copy was kept so re-requests (backfill) can find it.
	served, ok := findFileByID(fileID, "pic.png")
	if !ok {
		t.Fatal("served copy not kept — findFileByID missed")
	}
	t.Cleanup(func() { _ = os.Remove(served) }) //nolint

	// Exactly one GroupSend, carrying the file-reference envelope (not bytes).
	if len(fake.sent) != 1 || fake.sent[0].ID != "gid-9" {
		t.Fatalf("GroupSend calls = %+v, want one for gid-9", fake.sent)
	}
	meta, isFile := parseGroupFileText(fake.sent[0].Text)
	if !isFile {
		t.Fatalf("published text is not a file envelope: %q", fake.sent[0].Text)
	}
	if meta.ID != fileID || meta.Name != "pic.png" || meta.Size != int64(len("imgbytes")) {
		t.Errorf("published meta = %+v (fileID=%s)", meta, fileID)
	}
}

func TestSendFileToVisorGroup_StatFailsBeforePublish(t *testing.T) {
	fake := &groupAPI{}
	withFakePairRPC(t, fake)
	_, _, err := sendFileToVisorGroup(context.Background(), "g", filepath.Join(t.TempDir(), "nope.png"), "nope.png")
	if err == nil {
		t.Fatal("expected an error for a missing source file")
	}
	if len(fake.sent) != 0 {
		t.Errorf("nothing should be published when the source can't be stat'd; sent=%+v", fake.sent)
	}
}

// TestGroupHistory_EnrichesFileRow drives parseGroupFileText + enrichGroupFileRow
// through the real /group/<id>/history handler: a file-reference message is
// surfaced with a 📎 label and file_* fields, alongside untouched plain text.
func TestGroupHistory_EnrichesFileRow(t *testing.T) {
	fileText, err := encodeGroupFileText(groupFileMeta{ID: "hist-file-1", Name: "clip.mp4", Size: 7})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	fake := &groupAPI{history: []visor.GroupMessage{
		{GroupID: "g9", Text: "plain hello", TS: base},
		{GroupID: "g9", Text: fileText, TS: base.Add(time.Second)},
	}}
	withFakePairRPC(t, fake)

	rr := httptest.NewRecorder()
	groupItemHandler()(rr, httptest.NewRequest(http.MethodGet, "/group/g9/history?limit=10", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("history: code=%d body=%q", rr.Code, rr.Body.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d: %v", len(rows), rows)
	}
	// Row 0 is plain text — no file fields.
	if rows[0]["text"] != "plain hello" {
		t.Errorf("row0 text = %v, want plain hello", rows[0]["text"])
	}
	if _, ok := rows[0]["file_id"]; ok {
		t.Errorf("plain row should carry no file_id: %v", rows[0])
	}
	// Row 1 is the file reference — label + file fields, no file_url (not held).
	if rows[1]["text"] != "📎 clip.mp4" {
		t.Errorf("file row text = %v, want 📎 clip.mp4", rows[1]["text"])
	}
	if rows[1]["file_id"] != "hist-file-1" || rows[1]["file_name"] != "clip.mp4" {
		t.Errorf("file row fields wrong: %v", rows[1])
	}
	if _, ok := rows[1]["file_url"]; ok {
		t.Errorf("file_url should be absent (bytes not held locally): %v", rows[1])
	}
}
