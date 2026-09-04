// Package netscrape is a web browser written in Go/wasm. Its chrome — a tab
// strip, address bar, back/forward/reload — is DOM built with syscall/js; each
// tab is a sandboxed <iframe>. A page is fetched over a host-supplied transport
// (clearnet, or skywire's dmsg mesh), rendered into a sandboxed srcdoc with its
// stylesheets and images inlined, and its navigation relayed back to the chrome.
// The browser is Go; only the rendering (the iframe) and the network (the
// transport) are delegated.
//
// The browser is a LIBRARY: a host compiles it into its own Go/wasm binary and
// calls netscrape.Open(element) once (see browser.go, built for js/wasm), so it
// shares that binary's Go runtime — no separate module, no second runtime. The
// standalone binary (cmd/browser) is a thin wrapper for hosts that would rather
// serve or exec it as its own wasm; its pre-built blob and loader live in the
// dist subpackage.
//
// The previous JavaScript engine (browse.js, the SkywireBrowse panel) lives on
// the `js` branch.
package netscrape
