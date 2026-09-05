//go:build !tinygo && !(js && wasm)

package network

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// stacksContain reports whether any live goroutine stack mentions needle.
func stacksContain(needle string) bool {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return strings.Contains(string(buf[:n]), needle)
		}
		buf = make([]byte, 2*len(buf))
	}
}

// TestPeerConnectionSpawnsNoMDNSOrInterceptorGoroutines pins the two savings in
// newWebRTCAPI. Both were paid silently: pion defaults an unset mDNS mode to
// QueryOnly, and NewAPI registers the DEFAULT interceptors unless an interceptor
// registry is supplied — passing only WithMediaEngine does not suppress them.
// Together they were 8,816 goroutines (49%) on a host visor with 460 live
// PeerConnections, so a regression here is expensive and invisible.
func TestPeerConnectionSpawnsNoMDNSOrInterceptorGoroutines(t *testing.T) {
	api := newWebRTCAPI()
	pc, err := api.NewPeerConnection(webrtcConfig(nil))
	if err != nil {
		t.Fatalf("NewPeerConnection: %v", err)
	}
	defer func() { _ = pc.Close() }() //nolint:errcheck

	// A DataChannel drives the same setup path a real carrier uses; creating one
	// makes the ICE agent gather, which is what would start mDNS.
	if _, err := pc.CreateDataChannel(dcLabel, nil); err != nil {
		t.Fatalf("CreateDataChannel: %v", err)
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatalf("SetLocalDescription: %v", err)
	}

	// Gathering starts asynchronously; give the goroutines a moment to appear.
	time.Sleep(250 * time.Millisecond)

	for _, needle := range []string{
		"github.com/pion/mdns",
		"github.com/pion/interceptor",
	} {
		if stacksContain(needle) {
			t.Errorf("PeerConnection spawned %s goroutines; "+
				"newWebRTCAPI must keep mDNS disabled and the interceptor registry empty", needle)
		}
	}
}

// TestMDNSEnabledDefaultsOff documents that the goroutine saving is the default
// and that a deployment needing same-LAN browser peers can opt back in.
func TestMDNSEnabledDefaultsOff(t *testing.T) {
	t.Setenv("SKYWIRE_WEBRTC_MDNS", "")
	if mdnsEnabled() {
		t.Error("mDNS must default to disabled")
	}
	t.Setenv("SKYWIRE_WEBRTC_MDNS", "1")
	if !mdnsEnabled() {
		t.Error("SKYWIRE_WEBRTC_MDNS=1 must re-enable mDNS")
	}
}
