// Package visor pkg/visor/init_voice.go c3-vis-core
//
// Brings up the skychat 1:1 voice-call manager (pkg/skychat/voice): it listens
// for inbound call signaling on skyenv.SkychatVoiceSignalPort over BOTH dmsg AND
// skynet (same port), and dials outbound calls over whichever network resolves.
// Media (RTP) rides the signaling conn (a Noise-encrypted skywire transport),
// never a raw-internet/ICE path — see docs/skychat-voice-rfc.md.
package visor

import (
	"context"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	dmsgpkg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	skyvoice "github.com/skycoin/skywire/pkg/skychat/voice"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// initVoice wires the voice manager. Best-effort: with no dmsg client it's a
// no-op (voice disabled), like grouping/pairing.
func initVoice(_ context.Context, v *Visor, log *logging.Logger) error {
	if v.dmsgC == nil {
		return nil
	}
	localPK := v.conf.PK
	port := skyenv.SkychatVoiceSignalPort

	// Dial prefers skynet (on-mesh, IP-anonymous, multi-hop) when its networker
	// is up, and falls back to a direct dmsg stream. Both target the SAME port.
	dial := func(dctx context.Context, peer cipher.PubKey, p uint16) (net.Conn, error) {
		if _, err := appnet.ResolveNetworker(appnet.TypeSkynet); err == nil {
			addr := appnet.Addr{Net: appnet.TypeSkynet, PubKey: peer, Port: routing.Port(p)}
			if c, derr := appnet.DialContext(dctx, addr); derr == nil {
				return c, nil
			}
		}
		return v.dmsgC.DialStream(dctx, dmsgpkg.Addr{PK: peer, Port: p})
	}

	mgr := skyvoice.NewManager(skyvoice.Config{
		LocalPK:    localPK,
		Dial:       dial,
		SignalPort: port,
		// Headless auto-answer for now: media is silent/null until the audio
		// backend lands, so accepting an inbound call leaks no audio. A UI
		// ring/consent hook replaces this before real mic capture ships.
		OnIncoming: func(inv skyvoice.Sig) bool {
			log.WithField("from", inv.FromPK.Hex()).Info("voice: inbound call — auto-answering (headless)")
			return true
		},
		Logger: log,
	})
	v.voice = mgr

	// dmsg signaling listener on the shared port, now.
	var listeners []net.Listener
	if dl, err := v.dmsgC.Listen(port); err != nil {
		log.WithError(err).Warn("voice: dmsg signaling listen failed")
	} else {
		listeners = append(listeners, dl)
	}

	serveCtx, cancel := context.WithCancel(context.Background())
	go mgr.Serve(serveCtx, listeners...)

	// The skynet networker registers after dmsg; join its listener on the SAME
	// port once it's up (best-effort, bounded wait).
	go func() {
		for i := 0; i < 30; i++ {
			if serveCtx.Err() != nil {
				return
			}
			if _, err := appnet.ResolveNetworker(appnet.TypeSkynet); err == nil {
				addr := appnet.Addr{Net: appnet.TypeSkynet, PubKey: localPK, Port: routing.Port(port)}
				if sl, lerr := appnet.ListenContext(serveCtx, addr); lerr == nil {
					mgr.AddListener(serveCtx, sl)
					log.Debug("voice: skynet signaling listener up")
					return
				}
			}
			time.Sleep(time.Second)
		}
	}()

	v.pushCloseStack("voice", func() error { cancel(); return nil })
	log.WithField("port", port).Info("voice: 1:1 call manager up (dmsg+skynet signaling)")
	return nil
}
