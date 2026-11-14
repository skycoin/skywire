package rewardsserver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBroadcastAndRecord(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "2026-11-06.txt")
	sent := 0
	send := func() (string, error) { sent++; return "txid\n", nil }

	// A failed broadcast leaves nothing behind, so the day can be retried.
	if _, err := broadcastAndRecord(marker, func() (string, error) { return "", errors.New("node down") }); err == nil {
		t.Fatal("broadcast error was dropped")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 { //nolint:errcheck
		t.Fatalf("a failed broadcast left %d file(s)", len(entries))
	}

	txid, err := broadcastAndRecord(marker, send)
	if err != nil || txid != "txid\n" || sent != 1 {
		t.Fatalf("txid %q, err %v, sent %d", txid, err, sent)
	}
	if b, _ := os.ReadFile(marker); string(b) != "txid\n" { //nolint:errcheck,gosec
		t.Fatalf("marker holds %q", b)
	}
	if undistributed(dir, "2026-11-06") {
		t.Fatal("day still reads as undistributed")
	}
	if _, err := os.Stat(marker + ".pending"); err == nil {
		t.Fatal("reservation left behind")
	}
}

func TestBroadcastAndRecordStopsBeforeSendingTwice(t *testing.T) {
	sent := 0
	send := func() (string, error) { sent++; return "txid\n", nil }

	// A reservation left by a send that never finished blocks the next one.
	dir := t.TempDir()
	marker := filepath.Join(dir, "2026-11-06.txt")
	if err := os.WriteFile(marker+".pending", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := broadcastAndRecord(marker, send); err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("unfinished send: %v", err)
	}

	// A marker that cannot be written stops the broadcast.
	if _, err := broadcastAndRecord(filepath.Join(dir, "missing", "2026-11-07.txt"), send); err == nil {
		t.Fatal("unwritable marker: no error")
	}
	if sent != 0 {
		t.Fatalf("broadcast %d time(s) without a marker to record it in", sent)
	}
}
