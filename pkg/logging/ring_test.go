// Package logging pkg/logging/ring_test.go c0-com-log
package logging

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func fireEntry(b *Broadcaster, level logrus.Level, module, msg string, fields logrus.Fields) {
	data := logrus.Fields{}
	if module != "" {
		data[logModuleKey] = module
	}
	for k, v := range fields {
		data[k] = v
	}
	_ = b.Fire(&logrus.Entry{
		Time:    time.Now(),
		Level:   level,
		Message: msg,
		Data:    data,
	})
}

func TestRingClassifiesEventsAndLogs(t *testing.T) {
	b := NewBroadcaster()

	// Layered router entry tagged with app_name → events bucket.
	fireEntry(b, logrus.InfoLevel, "router", "Requesting new routes", logrus.Fields{"app_name": "skysocks-client"})
	// RouteGroup entry tagged with app_name → events bucket.
	fireEntry(b, logrus.DebugLevel, "RouteGroup abc", "Sent handshake via transport", logrus.Fields{"app_name": "skysocks-client"})
	// App's own stdout (proc:<app>:<key>) → logs bucket.
	fireEntry(b, logrus.InfoLevel, "proc:skysocks-client:xyz", "App is Running", nil)
	// Unrelated entry with no app association → dropped.
	fireEntry(b, logrus.InfoLevel, "dmsg", "some dmsg thing", nil)

	events, logs := b.RecentByApp("skysocks-client", logrus.DebugLevel)
	if len(events) != 2 {
		t.Fatalf("want 2 events, got %d: %+v", len(events), events)
	}
	if len(logs) != 1 {
		t.Fatalf("want 1 log, got %d: %+v", len(logs), logs)
	}
	if !events[0].IsEvent || events[1].IsEvent == false {
		t.Fatalf("events should be marked IsEvent")
	}
	if logs[0].IsEvent {
		t.Fatalf("log line should not be marked IsEvent")
	}

	// Nothing captured for an unknown app.
	e2, l2 := b.RecentByApp("other", logrus.DebugLevel)
	if len(e2) != 0 || len(l2) != 0 {
		t.Fatalf("unknown app should be empty, got %d/%d", len(e2), len(l2))
	}
}

func TestRingLevelFilter(t *testing.T) {
	b := NewBroadcaster()
	fireEntry(b, logrus.TraceLevel, "router", "trace line", logrus.Fields{"app_name": "app1"})
	fireEntry(b, logrus.InfoLevel, "router", "info line", logrus.Fields{"app_name": "app1"})

	// At info level, the trace entry is excluded.
	events, _ := b.RecentByApp("app1", logrus.InfoLevel)
	if len(events) != 1 {
		t.Fatalf("info filter: want 1 event, got %d", len(events))
	}
	// At trace level, both are included.
	events, _ = b.RecentByApp("app1", logrus.TraceLevel)
	if len(events) != 2 {
		t.Fatalf("trace filter: want 2 events, got %d", len(events))
	}
}

func TestRingBounded(t *testing.T) {
	b := NewBroadcaster()
	for i := 0; i < ringPerApp+50; i++ {
		fireEntry(b, logrus.InfoLevel, "router", "line", logrus.Fields{"app_name": "app1"})
	}
	events, _ := b.RecentByApp("app1", logrus.DebugLevel)
	if len(events) != ringPerApp {
		t.Fatalf("ring should cap at %d, got %d", ringPerApp, len(events))
	}
}

func TestRingMaxAppsEviction(t *testing.T) {
	b := NewBroadcaster()
	for i := 0; i < ringMaxApps+5; i++ {
		app := "app" + string(rune('A'+i%26)) + string(rune('0'+i/26))
		fireEntry(b, logrus.InfoLevel, "router", "line", logrus.Fields{"app_name": app})
	}
	b.ringMu.Lock()
	n := len(b.rings)
	b.ringMu.Unlock()
	if n > ringMaxApps {
		t.Fatalf("tracked apps should cap at %d, got %d", ringMaxApps, n)
	}
}

func TestRecordFormat(t *testing.T) {
	r := Record{
		Time:    time.Unix(0, 0).UTC(),
		Level:   logrus.InfoLevel,
		Module:  "router",
		Message: "Created new routes",
		Fields:  map[string]string{"app_name": "skysocks-client"},
	}
	line := r.Format()
	if line == "" {
		t.Fatalf("Format returned empty line")
	}
	if got := line[len(line)-1]; got == '\n' {
		t.Fatalf("Format should not end with newline")
	}
}

func TestRecentMergedOrdered(t *testing.T) {
	b := NewBroadcaster()
	base := time.Now()
	// Interleave events and logs with explicit times.
	push := func(mod, msg string, fields logrus.Fields, at time.Time) {
		data := logrus.Fields{logModuleKey: mod}
		for k, v := range fields {
			data[k] = v
		}
		_ = b.Fire(&logrus.Entry{Time: at, Level: logrus.InfoLevel, Message: msg, Data: data})
	}
	push("proc:app1:k", "log-early", nil, base)
	push("router", "event-mid", logrus.Fields{"app_name": "app1"}, base.Add(time.Second))
	push("proc:app1:k", "log-late", nil, base.Add(2*time.Second))

	merged := b.RecentMerged("app1", logrus.DebugLevel)
	if len(merged) != 3 {
		t.Fatalf("want 3 merged, got %d", len(merged))
	}
	if merged[0].Message != "log-early" || merged[1].Message != "event-mid" || merged[2].Message != "log-late" {
		t.Fatalf("merged not time-ordered: %s, %s, %s", merged[0].Message, merged[1].Message, merged[2].Message)
	}
}
