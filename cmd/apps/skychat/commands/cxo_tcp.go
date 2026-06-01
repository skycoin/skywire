// Package commands — cxo_tcp.go: CXO-backed messaging over the CXO
// node's NATIVE TCP transport (no dmsg). Opt-in via --cxo; works in
// --standalone. Outbound /message is published to our own CXO feed;
// each --cxo-peer feed is subscribed and surfaced on the SSE stream,
// reusing the same /message + /sse surfaces as the tcp-direct bridge.
//
// A 1:1 P2P pair is just a two-member group on this publish/subscribe
// mechanism, so groups (docs/skychat_cxo_tcp_standalone.md, Phase 2)
// extend this by adding more --cxo-peer subscriptions to a shared feed.
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
)

var (
	cxoEnable bool
	cxoListen string
	cxoPeers  []string

	cxoMu   sync.Mutex
	cxoPub  *treestore.Publisher
	cxoMyPK cipher.PubKey
	cxoSubs []*treestore.Subscriber
	cxoSeq  uint64
)

// startCXOTCP brings up CXO-over-native-TCP messaging when --cxo is set.
// It publishes our feed on --cxo-listen and subscribes to every
// --cxo-peer feed, bridging received messages onto the SSE hub. Mirrors
// startTCPDirect; fully opt-in (returns nil when --cxo is unset). The
// identity (feed PK) is resolved exactly like tcp-direct (--sk / env /
// -c config), so the CXO feed PK equals the chat identity.
func startCXOTCP(ctx context.Context) error {
	if !cxoEnable {
		return nil
	}
	pk, sk, source, err := resolveTCPIdentity()
	if err != nil {
		return fmt.Errorf("cxo: %w", err)
	}
	pub, err := treestore.NewWithTCP(cxoListen, sk, treestore.PubConfig{
		InMemoryDB:  true,
		BatchWindow: 300 * time.Millisecond,
	})
	if err != nil {
		return fmt.Errorf("cxo: publisher on %s: %w", cxoListen, err)
	}
	cxoMu.Lock()
	cxoPub = pub
	cxoMyPK = pk
	cxoMu.Unlock()
	appLog("skychat: cxo feed publishing as pk=%s on %s (identity source: %s)", pk, cxoListen, source)

	for _, spec := range cxoPeers {
		feedPK, addr, perr := parseCXOPeer(spec)
		if perr != nil {
			appLog("skychat: cxo skipping invalid --cxo-peer %q: %v", spec, perr)
			continue
		}
		sub, serr := treestore.NewSubscriberTCP("", feedPK, treestore.SubConfig{InMemoryDB: true})
		if serr != nil {
			appLog("skychat: cxo subscriber for %s failed: %v", feedPK, serr)
			continue
		}
		fpk := feedPK.Hex()
		sub.OnUpdate(func(events []treestore.UpdateEvent) {
			for _, ev := range events {
				if ev.Value == nil {
					continue
				}
				surfaceCXOInbound(fpk, string(ev.Value))
			}
		})
		cxoMu.Lock()
		cxoSubs = append(cxoSubs, sub)
		cxoMu.Unlock()
		// Retry the initial dial until it connects — the peer may not be
		// listening yet at startup. ConnectTCP arms the reconnect
		// watchdog only after a first success, so a failed initial dial
		// would otherwise never retry; the watchdog handles re-connects
		// once we are connected.
		go cxoDialUntilConnected(ctx, sub, addr, fpk)
	}

	go func() {
		<-ctx.Done()
		cxoMu.Lock()
		defer cxoMu.Unlock()
		for _, s := range cxoSubs {
			_ = s.Close() //nolint:errcheck
		}
		if cxoPub != nil {
			_ = cxoPub.Close() //nolint:errcheck
		}
	}()
	return nil
}

// cxoDialUntilConnected retries Subscriber.ConnectTCP with capped
// exponential backoff until the first successful connect (the peer may
// not be listening yet at startup). After success the subscriber's
// reconnect watchdog takes over, so this returns.
func cxoDialUntilConnected(ctx context.Context, sub *treestore.Subscriber, addr, fpk string) {
	backoff := time.Second
	for {
		err := sub.ConnectTCP(ctx, addr)
		if err == nil {
			appLog("skychat: cxo subscribed to feed %s via %s", fpk, addr)
			return
		}
		appLog("skychat: cxo dial %s (%s) failed, retry in %s: %v", addr, fpk, backoff, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// publishCXO publishes a chat message body to our own CXO feed. Every
// peer subscribed to our feed receives it. Returns the leaf path used.
func publishCXO(body string) (string, error) {
	cxoMu.Lock()
	pub := cxoPub
	cxoMu.Unlock()
	if pub == nil {
		return "", fmt.Errorf("cxo publisher not ready")
	}
	seq := atomic.AddUint64(&cxoSeq, 1)
	// Timestamp-nano + seq keeps paths lexically ordered and unique even
	// for two messages within the same nanosecond.
	path := fmt.Sprintf("msgs/%019d-%06d", time.Now().UTC().UnixNano(), seq)
	if err := pub.Put(path, []byte(body)); err != nil {
		return "", err
	}
	return path, nil
}

// surfaceCXOInbound pushes a message received from a subscribed CXO feed
// onto the SSE hub, mirroring handleConn's inbound clientMsg shape.
func surfaceCXOInbound(senderPK, text string) {
	counterMu.Lock()
	inboundMsgCount++
	lastRxAt = time.Now().UTC()
	counterMu.Unlock()

	clientMsg, err := json.Marshal(map[string]interface{}{
		"sender":  senderPK,
		"message": text,
		"network": "cxo",
		"dir":     "in",
		"id":      newEventID(),
		"len":     len(text),
	})
	if err != nil {
		appLog("skychat: cxo inbound marshal failed: %v", err)
		return
	}
	hub.broadcast(string(clientMsg))
}

// parseCXOPeer parses "tcp://<feedpk>@<host:port>".
func parseCXOPeer(spec string) (cipher.PubKey, string, error) {
	s := strings.TrimPrefix(spec, "tcp://")
	at := strings.IndexByte(s, '@')
	if at <= 0 {
		return cipher.PubKey{}, "", fmt.Errorf("expected tcp://<feedpk>@<host:port>")
	}
	var pk cipher.PubKey
	if err := pk.Set(s[:at]); err != nil {
		return cipher.PubKey{}, "", fmt.Errorf("invalid feed pk: %w", err)
	}
	addr := s[at+1:]
	if addr == "" {
		return cipher.PubKey{}, "", fmt.Errorf("missing host:port")
	}
	return pk, addr, nil
}
