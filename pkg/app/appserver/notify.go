// Package appserver pkg/app/appserver/notify.go c2-vis-appsvc
//
// The app→visor half of the visor-global notification system.
//
// An app decides *whether* an event deserves the user's attention — only
// skychat knows which thread is focused and which are muted, only vpn-client
// knows a reconnect matters. It then publishes a NotifyReq and is done. The
// visor decides *where* that lands (an attached UI, a subscribed host app such
// as the Android service, the host-OS notification center, or nowhere at all on
// a headless box). Apps therefore carry no OS-specific notification code.
//
// This file deliberately holds only the wire type and a one-method interface:
// pkg/app/appserver must keep compiling for GOOS=js/wasm and TinyGo (the
// browser wasm-visor imports it), so every transport concern — net/http, SSE,
// os/exec — lives on the pkg/visor side, which never builds for those targets.
package appserver

// NotifyReq is a single user-facing notification published by an app.
//
// All fields are plain strings so the value gob-encodes on both the reflection
// and the TinyGo codec paths without a gob.Register.
type NotifyReq struct {
	// App is the publishing app's name (e.g. "skychat"). Apps may leave it
	// empty: the visor stamps it from the proc's own identity and overwrites
	// whatever arrived on the wire, so an app cannot publish under another
	// app's name (see RPCIngressGateway.Notify).
	App string
	// Title is the bold heading.
	Title string
	// Body is the message text. Frequently UNTRUSTED — a peer's chat message —
	// so it is never logged and never interpolated into anything executable.
	Body string
	// Tag optionally groups related notifications so a sink that supports
	// coalescing (an Android notification tag, say) can replace rather than
	// stack them. Empty means "no coalescing". Sinks without the concept —
	// pkg/osnotify — ignore it.
	Tag string
}

// NotificationHub receives notifications published by apps and routes them to
// whatever sink can actually reach the user. It is implemented on the visor
// side (*visor.NotifyHub); appserver only needs the seam.
//
// Publish MUST NOT block: it is called on the publishing app's single RPC serve
// goroutine, so a slow sink would stall that app's Dial/Write/Read too.
type NotificationHub interface {
	Publish(n NotifyReq)
}
