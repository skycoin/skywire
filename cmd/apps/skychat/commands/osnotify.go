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
	if !shouldNotifyOS(osNotify, uiCanNotify()) {
		return
	}
	go deliverNotification(title, body)
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
func deliverNotification(title, body string) {
	if appCl != nil {
		appCl.NotifyOrLog(title, body, "")
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
