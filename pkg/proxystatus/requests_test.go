package proxystatus

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRequestLogKeepsNewestFirst(t *testing.T) {
	l := NewRequestLog(3)
	for _, h := range []string{"a", "b", "c", "d"} {
		layer := "dmsg"
		if h == "c" {
			layer = "skynet"
		}
		l.Add(Request{Host: h, Layer: layer})
	}
	got := l.Recent("")
	if len(got) != 3 || got[0].Host != "d" || got[1].Host != "c" || got[2].Host != "b" {
		t.Fatalf("Recent: %+v", got)
	}
	if got := l.Recent("dmsg"); len(got) != 2 || got[0].Host != "d" || got[1].Host != "b" {
		t.Fatalf("Recent(dmsg): %+v", got)
	}
	var nilLog *RequestLog
	nilLog.Add(Request{})
	if nilLog.Recent("") != nil {
		t.Fatal("nil log returned requests")
	}
}

func TestNotePath(t *testing.T) {
	NotePath(context.Background(), "ignored")
	ctx, note := WithPathNote(context.Background())
	NotePath(ctx, "skynet direct transport")
	if note.String() != "skynet direct transport" {
		t.Fatalf("note = %q", note.String())
	}
}

func TestRenderRequests(t *testing.T) {
	page := string(Render(Snapshot{Surface: SurfaceSkywire, Requests: []Request{
		{At: time.Now(), Layer: "dmsg", Host: "status.dmsg", Port: 80, Path: "dmsg via 02ab over skynet", DialMS: 12},
		{At: time.Now(), Layer: "skynet", Host: "x.skynet", Port: 443, Err: "no route"},
	}}))
	for _, want := range []string{"requests <small>2 recent</small>", "status.dmsg:80", "dmsg via 02ab over skynet", "no route", "12ms"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}
