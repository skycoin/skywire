// Package commands cmd/apps/skychat/commands/osnotify.go c4-app-chat
//
// Host-OS desktop notifications for inbound messages — the background-process
// complement to the browser's Web Notifications (static/index.html). The two
// never double up: the browser owns notifications whenever a UI that CAN show
// them is attached (it alone knows which thread is focused + per-thread mutes);
// the Go process fills the gap when NO capable UI is present — the UI is closed,
// or it's open but the browser blocked/disabled notifications.
//
// "A capable UI is present" combines two signals:
//   - SSE client count (hub.clientCount) — instant: a closed tab drops to 0, so
//     the Go side takes over immediately, no waiting on a lease.
//   - A capability lease refreshed by POST /notify-capable {capable:true}. The
//     browser heartbeats this only while it can actually notify; when it can't
//     (permission denied / notifications turned off) it stops (or posts
//     capable:false) and the lease goes stale, so the Go side resumes.
//
// Once past that gate skychat is done deciding: it publishes to the VISOR's
// notification hub (pkg/visor/notifyhub.go) and the hub picks the sink that can
// actually reach the user — a subscribed host app (the Android service), the
// host OS notification center, or nothing at all on a headless visor. The split
// is deliberate: the app knows *whether* an event deserves attention (focus,
// mutes), the visor knows *where* the user can be reached.
//
// Note the gate no longer consults osnotify.Available(). It cannot: on Android
// there is no desktop session, yet the host-app sink is exactly the one that
// must fire. Dropping it is behavior-neutral on the desktop because
// osnotify.Notify re-checks availability itself.
//
// --standalone is the one exception: with no visor there is no hub to publish
// to, so that mode keeps posting to the host OS directly.
package commands

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/osnotify"
)

// osNotify enables host-OS desktop notifications (the --os-notify flag). On by
// default; harmlessly no-ops wherever no desktop session exists.
var osNotify = true

// notifyCapableTTL is how long a browser's "I can notify" heartbeat keeps the
// capability lease fresh. The browser refreshes well within this window; once
// it stops (tab closed / notifications blocked) the lease expires and the Go
// side resumes posting.
const notifyCapableTTL = 45 * time.Second

var (
	notifyCapMu sync.Mutex
	notifyCapAt time.Time // last time a connected UI reported it can notify
)

// notifyFocusTTL is how long a "the user is looking at conversation X" report
// stays believable without being refreshed. Short on purpose: the reporter is
// a web page, and a page that gets backgrounded (an Android WebView above all)
// has its timers frozen — the clear it would send never arrives, so the lease
// expiring is what turns notifications back on. The page refreshes every few
// seconds while focused.
const notifyFocusTTL = 15 * time.Second

var (
	notifyFocusMu  sync.Mutex
	notifyFocusKey string    // conversation an attached UI is showing ("" = none)
	notifyFocusAt  time.Time // when that was last affirmed
)

// markUIFocus records (or clears) which conversation an attached UI is
// actively displaying.
func markUIFocus(key string, focused bool) {
	notifyFocusMu.Lock()
	if focused && key != "" {
		notifyFocusKey, notifyFocusAt = key, time.Now()
	} else {
		notifyFocusKey, notifyFocusAt = "", time.Time{}
	}
	notifyFocusMu.Unlock()
}

// uiShowingThread reports whether an attached UI is displaying conversation
// `key` right now: at least one SSE client, a matching focus report, and the
// report still fresh. This is the gate that matters on Android, where the
// WebView cannot own notifications (no Notifications API) so the capability
// lease never suppresses anything — without this, a message lands as a system
// notification while the user is looking at the very chat it belongs to.
func uiShowingThread(key string) bool {
	if key == "" || hub == nil || hub.clientCount() == 0 {
		return false
	}
	notifyFocusMu.Lock()
	defer notifyFocusMu.Unlock()
	return notifyFocusKey == key && time.Since(notifyFocusAt) < notifyFocusTTL
}

// notifyFocusHandler serves POST /notify-focus {key, focused}: the UI reports
// the conversation it is actively showing (group ID or peer PK hex), driving
// the lease uiShowingThread() reads.
func notifyFocusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Key     string `json:"key"`
		Focused bool   `json:"focused"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	markUIFocus(body.Key, body.Focused)
	w.WriteHeader(http.StatusNoContent)
}

// markUINotifyCapable refreshes (capable) or clears (!capable) the lease.
func markUINotifyCapable(capable bool) {
	notifyCapMu.Lock()
	if capable {
		notifyCapAt = time.Now()
	} else {
		notifyCapAt = time.Time{}
	}
	notifyCapMu.Unlock()
}

// uiCanNotify reports whether a UI able to show notifications is attached: at
// least one SSE client connected AND a fresh capability lease.
func uiCanNotify() bool {
	if hub == nil || hub.clientCount() == 0 {
		return false
	}
	notifyCapMu.Lock()
	fresh := !notifyCapAt.IsZero() && time.Since(notifyCapAt) < notifyCapableTTL
	notifyCapMu.Unlock()
	return fresh
}

// notifyCapableHandler serves POST /notify-capable {capable:bool}: the browser
// reports whether it can currently show notifications, driving the capability
// lease uiCanNotify() reads.
func notifyCapableHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Capable bool `json:"capable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	markUINotifyCapable(body.Capable)
	w.WriteHeader(http.StatusNoContent)
}

// logOSNotifyStartup logs once whether host-OS notifications will actually
// fire, so an operator can tell at a glance — and it warms the availability
// cache off the hot path.
func logOSNotifyStartup() {
	if !osNotify || appLog == nil {
		return
	}
	if osnotify.Available() {
		appLog("Host-OS desktop notifications: enabled (fire when no browser UI is attached)")
	} else {
		appLog("Host-OS desktop notifications: no desktop session detected — will no-op")
	}
}

// notifyOSInbound surfaces an inbound message when no capable UI is attached
// (see the file header). Best-effort + async: delivery runs in a goroutine so
// neither an RPC round-trip nor a slow notifier delays message handling.
//
// `!osNotify` stays the first clause — TestMain flips that single flag to keep
// the suite from posting real notifications on a developer's desktop.
func notifyOSInbound(title, body string) {
	notifyOSInboundThread("", "", title, body)
}

// notifyOSInboundThread is notifyOSInbound for a message that belongs to a
// specific conversation. threadKey is the thread identity the UI itself uses
// (group ID or peer PK hex): a message for the conversation on screen RIGHT
// NOW is not a notification, it is the screen. tag rides to the visor hub so
// sinks that coalesce by tag (the Android bridge) stack a conversation into
// one notification instead of piling them up. Both may be empty.
func notifyOSInboundThread(threadKey, tag, title, body string) {
	if !shouldNotifyOS(osNotify, uiCanNotify()) {
		// The gate is the first place a "why did I get no notification?"
		// investigation stops, and until now it left no trace at all — an
		// attached-but-capable UI and a disabled flag look identical from
		// outside. Debug only: this fires once per inbound message.
		if chatLog != nil {
			chatLog.WithField("os_notify", osNotify).
				WithField("ui_capable", uiCanNotify()).
				WithField("ui_clients", uiClientCount()).
				Debug("skychat: host-OS notification suppressed")
		}
		return
	}
	if uiShowingThread(threadKey) {
		if chatLog != nil {
			chatLog.WithField("thread", threadKey).
				Debug("skychat: notification suppressed — thread is on screen")
		}
		return
	}
	go deliverNotification(title, body, tag)
}

// uiClientCount reports how many browser UIs are attached, for the trace above:
// "a UI is connected but says it cannot notify" and "no UI at all" are
// different problems and the lease alone doesn't tell them apart.
func uiClientCount() int {
	if hub == nil {
		return 0
	}
	return hub.clientCount()
}

// shouldNotifyOS is the no-double-fire rule, split out so it can be pinned
// exhaustively by a test: the Go side delivers only when notifications are
// enabled AND no UI that can show them itself is attached. A capable UI owns
// the notification because it alone knows the focused thread and the mutes.
func shouldNotifyOS(enabled, uiCapable bool) bool {
	return enabled && !uiCapable
}

// deliverNotification hands the notification to the visor, which routes it to
// whichever sink can reach the user. In --standalone mode there is no visor
// (appCl is nil), so it posts to the host OS itself — today's path, unchanged.
func deliverNotification(title, body, tag string) {
	if appCl != nil {
		appCl.NotifyOrLog(title, body, tag)
		return
	}
	if !osnotify.Available() {
		return
	}
	err := osnotify.Notify(osnotify.Notification{Title: title, Body: body, AppName: "Skychat"})
	if err != nil && !errors.Is(err, osnotify.ErrUnavailable) && appLog != nil {
		appLog("skychat: os notification failed: %v", err)
	}
}

// notifPreviewLen bounds the message snippet shown in an OS notification.
const notifPreviewLen = 80

// shortHexPK renders a PK hex as a compact 8…4 label for a notification title.
func shortHexPK(hex string) string {
	if len(hex) >= 14 {
		return hex[:8] + "…" + hex[len(hex)-4:]
	}
	return hex
}

// fileKindLabel returns a short human label for a file's kind, for a file
// notification body ("Image", "Video", "Audio", "PDF", "Archive", "Document",
// or a generic "File"). Derived from the extension, falling back to the
// top-level MIME type when the extension is unknown.
func fileKindLabel(name, mimeType string) string {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")) {
	case "jpg", "jpeg", "png", "gif", "webp", "bmp", "svg", "heic", "tiff", "tif":
		return "Image"
	case "mp4", "m4v", "webm", "mov", "mkv", "avi", "ogv", "flv", "wmv":
		return "Video"
	case "mp3", "wav", "ogg", "oga", "opus", "m4a", "aac", "flac", "weba", "wma":
		return "Audio"
	case "pdf":
		return "PDF"
	case "zip", "tar", "gz", "tgz", "bz2", "xz", "7z", "rar", "zst":
		return "Archive"
	case "doc", "docx", "odt", "rtf", "txt", "md", "xls", "xlsx", "ods", "ppt", "pptx", "odp", "csv":
		return "Document"
	}
	if i := strings.IndexByte(mimeType, '/'); i > 0 {
		switch mimeType[:i] {
		case "image":
			return "Image"
		case "video":
			return "Video"
		case "audio":
			return "Audio"
		}
	}
	return "File"
}

// notifPreview collapses whitespace and truncates message text for a
// notification body.
func notifPreview(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > notifPreviewLen {
		return string(r[:notifPreviewLen]) + "…"
	}
	return s
}
