package call

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// A call whose devices failed to open says so, and why, for the whole call.
func TestCallDevicesReportFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	lis := newMemListener()
	defer lis.Close() //nolint:errcheck

	noMic := errors.New("voice: pulse connect: no server")
	noSpk := errors.New("voice: open playback stream: refused")

	mgrB := NewManager(Config{
		LocalPK:    pkB,
		Dial:       func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return nil, io.EOF },
		OnIncoming: func(Sig) bool { return true },
		NewSource:  func() Source { return &toneSource{} },
		NewSink:    func() Sink { return &captureSink{} },
	})
	go mgrB.Serve(ctx, lis)

	mgrA := NewManager(Config{
		LocalPK:   pkA,
		Dial:      func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return lis.dial() },
		Visualize: true,
		NewSource: func() Source { return UnavailableSource{Err: noMic} },
		NewSink:   func() Sink { return UnavailableSink{Err: noSpk} },
	})
	sess, err := mgrA.Call(ctx, pkB)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	got, err := mgrA.CallDevices(sess.CallID)
	if err != nil {
		t.Fatalf("CallDevices: %v", err)
	}
	want := DeviceStatus{MicErr: noMic.Error(), SpeakerErr: noSpk.Error()}
	if got != want {
		t.Fatalf("caller devices = %+v, want %+v", got, want)
	}

	deadline := time.Now().Add(time.Second)
	for len(mgrB.Active()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	ids := mgrB.Active()
	if len(ids) != 1 {
		t.Fatalf("callee active calls = %d, want 1", len(ids))
	}
	if got, err := mgrB.CallDevices(ids[0]); err != nil || got != (DeviceStatus{Mic: true, Speaker: true}) {
		t.Fatalf("callee devices = %+v, %v; want both real", got, err)
	}

	if err := mgrA.Hangup(sess.CallID); err != nil {
		t.Fatalf("hangup: %v", err)
	}
	waitNoActive(t, mgrA, "caller")
	if _, err := mgrA.CallDevices(sess.CallID); err == nil {
		t.Fatal("CallDevices of an ended call should fail")
	}
}
