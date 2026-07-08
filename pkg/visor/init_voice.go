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
	"os"
	"strings"
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

	cfg := skyvoice.Config{
		LocalPK:    localPK,
		Dial:       dial,
		SignalPort: port,
		Logger:     log,
	}

	// Opus is pure-Go (thesyncim/gopus — no cgo, no libopus, always available), so
	// use it for EVERY call — both the silent auto-answer path and the real-audio
	// path — so both ends always agree on the codec. Each session gets its own
	// (opus is stateful); fall back to PCM only if the encoder can't initialize.
	cfg.NewCodec = func() skyvoice.Codec {
		c, err := skyvoice.NewOpusCodec()
		if err != nil {
			return skyvoice.NewPCMCodec()
		}
		return c
	}

	// Voice ALWAYS uses explicit-answer: a visor never streams audio to a caller
	// until the call is accepted (the Answer button in the hvui, or `skychat
	// voice answer`). That privacy guarantee is what lets us enable real audio
	// BY DEFAULT — no env var or restart needed for voice chats to actually
	// carry audio. On a headless host with no audio device, capture/playback
	// degrade to silence and the call still connects (receive-only).
	//
	// SKYWIRE_VOICE_AUDIO only OVERRIDES the default:
	//   (unset)/mic       → the system default input device (a mic, or on a
	//                        desktop with no mic the monitor, per the OS default)
	//   monitor           → force the default sink's monitor (system audio)
	//   off/none/silent/0 → no local capture (receive-only), still explicit-answer
	//   auto/auto-answer  → auto-accept inbound calls, silent (headless/testing)
	ring := func(inv skyvoice.Sig) {
		log.WithField("from", inv.FromPK.Hex()).WithField("call", inv.CallID).
			Warn("voice: INCOMING CALL — answer in the hvui or `skywire cli skychat voice answer <id>`")
	}
	switch mode := strings.ToLower(strings.TrimSpace(os.Getenv("SKYWIRE_VOICE_AUDIO"))); mode {
	case "auto", "auto-answer", "autoanswer":
		// Headless/testing: accept every inbound call silently.
		cfg.OnIncoming = func(inv skyvoice.Sig) bool {
			log.WithField("from", inv.FromPK.Hex()).Info("voice: inbound call — auto-answering (silent)")
			return true
		}
	case "off", "none", "silent", "0", "false":
		cfg.ManualAnswer = true
		cfg.Ring = ring
		log.Info("voice: real audio OFF (SKYWIRE_VOICE_AUDIO) — calls ring; answer to connect (silent)")
	default:
		monitor := mode == "monitor"
		cfg.ManualAnswer = true
		cfg.Visualize = true // tap call audio for `voice spectrogram --call`
		cfg.NewSource = func() skyvoice.Source {
			s, err := skyvoice.NewMicSource(monitor, 0)
			if err != nil {
				log.WithError(err).Warn("voice: audio capture unavailable — using silence (call still connects)")
				return skyvoice.SilentSource{}
			}
			return s
		}
		cfg.NewSink = func() skyvoice.Sink {
			s, err := skyvoice.NewSpeakerSink(0)
			if err != nil {
				return skyvoice.NullSink{}
			}
			return s
		}
		cfg.Ring = ring
		srcKind := "system default input"
		if monitor {
			srcKind = "system-audio monitor"
		}
		log.Infof("voice: real audio enabled (%s) — calls ring; answer to connect", srcKind)
	}

	mgr := skyvoice.NewManager(cfg)
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
