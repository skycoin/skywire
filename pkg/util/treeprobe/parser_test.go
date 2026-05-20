package treeprobe

import (
	"io"
	"strings"
	"testing"
)

// Synthetic fixtures matching the wire shape from PR #2732's
// emitOne. The Int64String type accepts either JSON string (proto
// default) or JSON number (test convenience); both forms exercise
// the same code path.
const sampleStream = `
{"ts":"2026-05-20T01:30:00.100Z","type":"discovered","data":{"tp_id":"abc123","tp_type":"stcpr","remote_pk":"02deadbeef","parent_pk":"03self","level":1}}
{"ts":"2026-05-20T01:30:00.250Z","type":"ping_result","data":{"tp_id":"abc123","tp_type":"stcpr","remote_pk":"02deadbeef","parent_pk":"03self","level":1,"canceled":false,"sample_count":5,"setup_latency_ns":"170710214","ping_avg_ns":"287931103","ping_p50_ns":"290004599","ping_p99_ns":"290004599","jitter_ns":"2073496","failed":false,"latency_source":"live_ping"}}
{"ts":"2026-05-20T01:30:00.300Z","type":"ping_result","data":{"tp_id":"def456","tp_type":"sudph","remote_pk":"02cafefeed","parent_pk":"03self","level":1,"sample_count":1,"ping_avg_ns":"15000000","ping_p50_ns":"15000000","ping_p99_ns":"15000000","jitter_ns":"0","failed":false,"latency_source":"transport_summary"}}
{"ts":"2026-05-20T01:30:00.500Z","type":"level_done","data":{"level":1,"attempted":2,"succeeded":2,"failed":0,"skipped_cached":1}}
{"ts":"2026-05-20T01:30:05.000Z","type":"status_update","data":{"phase":"pinging_level_2","in_flight":4,"pending":12}}
{"ts":"2026-05-20T01:30:10.000Z","type":"run_done","data":{"total_discovered":20,"total_pinged":15,"total_succeeded":12,"total_failed":3,"total_skipped_cached":5,"wall_time_ns":"10000000000","peak_in_flight":16,"termination_reason":"hops_target"}}
`

func TestParser_Next_AllEventTypes(t *testing.T) {
	p := NewParser(strings.NewReader(sampleStream))

	// 1: discovered
	d, err := p.Next()
	if err != nil {
		t.Fatalf("event 1: %v", err)
	}
	if d.Type != TypeDiscovered || d.Discovered == nil {
		t.Fatalf("event 1: wrong type %q or nil payload", d.Type)
	}
	if d.Discovered.TpID != "abc123" || d.Discovered.Level != 1 {
		t.Errorf("event 1: discovered fields wrong: %+v", *d.Discovered)
	}

	// 2: ping_result (live_ping with all stats)
	d, err = p.Next()
	if err != nil {
		t.Fatalf("event 2: %v", err)
	}
	if d.Type != TypePingResult || d.PingResult == nil {
		t.Fatalf("event 2: wrong type or nil")
	}
	pr := d.PingResult
	if pr.SampleCount != 5 {
		t.Errorf("event 2: sample_count want 5 got %d", pr.SampleCount)
	}
	if int64(pr.PingAvgNs) != 287931103 {
		t.Errorf("event 2: ping_avg_ns want 287931103 got %d", pr.PingAvgNs)
	}
	if int64(pr.JitterNs) != 2073496 {
		t.Errorf("event 2: jitter_ns want 2073496 got %d", pr.JitterNs)
	}
	if pr.LatencySource != "live_ping" {
		t.Errorf("event 2: latency_source want live_ping got %q", pr.LatencySource)
	}

	// 3: ping_result with latency_source=transport_summary (fast-path)
	d, err = p.Next()
	if err != nil {
		t.Fatalf("event 3: %v", err)
	}
	if d.PingResult.LatencySource != "transport_summary" {
		t.Errorf("event 3: latency_source want transport_summary got %q", d.PingResult.LatencySource)
	}
	if d.PingResult.SampleCount != 1 {
		t.Errorf("event 3: fast-path sample_count want 1 got %d", d.PingResult.SampleCount)
	}

	// 4: level_done with skipped_cached
	d, err = p.Next()
	if err != nil {
		t.Fatalf("event 4: %v", err)
	}
	if d.Type != TypeLevelDone {
		t.Fatalf("event 4: wrong type %q", d.Type)
	}
	if d.LevelDone.SkippedCached != 1 {
		t.Errorf("event 4: skipped_cached want 1 got %d", d.LevelDone.SkippedCached)
	}

	// 5: status_update
	d, err = p.Next()
	if err != nil {
		t.Fatalf("event 5: %v", err)
	}
	if d.Type != TypeStatusUpdate || d.StatusUpdate.Phase != "pinging_level_2" {
		t.Errorf("event 5: status wrong: %+v", d)
	}

	// 6: run_done
	d, err = p.Next()
	if err != nil {
		t.Fatalf("event 6: %v", err)
	}
	if d.Type != TypeRunDone {
		t.Fatalf("event 6: wrong type %q", d.Type)
	}
	if int64(d.RunDone.WallTimeNs) != 10000000000 {
		t.Errorf("event 6: wall_time_ns want 10000000000 got %d", d.RunDone.WallTimeNs)
	}
	if d.RunDone.TerminationReason != "hops_target" {
		t.Errorf("event 6: termination_reason wrong: %q", d.RunDone.TerminationReason)
	}
	if d.RunDone.TotalDiscovered != 20 || d.RunDone.TotalSkippedCached != 5 {
		t.Errorf("event 6: totals wrong: discovered=%d skipped_cached=%d",
			d.RunDone.TotalDiscovered, d.RunDone.TotalSkippedCached)
	}

	// EOF
	d, err = p.Next()
	if err != io.EOF {
		t.Fatalf("want EOF after 6 events; got d=%+v err=%v", d, err)
	}
}

func TestParser_TolerantToBlankLines(t *testing.T) {
	stream := "\n  \n\t\n{\"ts\":\"t\",\"type\":\"server_error\",\"data\":{\"code\":\"X\",\"message\":\"y\"}}\n\n"
	p := NewParser(strings.NewReader(stream))
	d, err := p.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if d.Type != TypeServerError || d.ServerError.Code != "X" {
		t.Errorf("blank-line skip failed: %+v", d)
	}
}

func TestParser_RejectsUnknownType(t *testing.T) {
	stream := `{"ts":"t","type":"weird_new_event","data":{}}` + "\n"
	p := NewParser(strings.NewReader(stream))
	_, err := p.Next()
	if err == nil {
		t.Fatal("want error on unknown type; got nil")
	}
}

func TestParser_RejectsMalformedJSON(t *testing.T) {
	stream := `{"ts":"t","type":"discovered","data":{`
	p := NewParser(strings.NewReader(stream))
	_, err := p.Next()
	if err == nil {
		t.Fatal("want error on malformed JSON; got nil")
	}
}

func TestInt64String_TolerantOfNumberForm(t *testing.T) {
	// Hand-written test fixtures may use raw numbers; production
	// protobuf wire is always string. Both must parse.
	var i Int64String
	if err := i.UnmarshalJSON([]byte(`"123"`)); err != nil {
		t.Fatalf("string form: %v", err)
	}
	if i != 123 {
		t.Errorf("string form: want 123 got %d", i)
	}
	if err := i.UnmarshalJSON([]byte(`456`)); err != nil {
		t.Fatalf("number form: %v", err)
	}
	if i != 456 {
		t.Errorf("number form: want 456 got %d", i)
	}
}

func TestInt64String_RoundTrip(t *testing.T) {
	original := Int64String(42)
	b, err := original.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `"42"` {
		t.Errorf("marshal: want %q got %q", `"42"`, string(b))
	}
	var back Int64String
	if err := back.UnmarshalJSON(b); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != original {
		t.Errorf("round-trip: want %d got %d", original, back)
	}
}
