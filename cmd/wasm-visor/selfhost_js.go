//go:build js && wasm

// Package main cmd/wasm-visor/selfhost_js.go c3-vis-wasm
//
// The pairing half of fetchDmsg/browsing: a wasm-visor can HOST a small site
// (HTML/CSS/...) addressed by its PK, served over dmsg with a minimal net/http-
// free HTTP/1.1 server on dmsg port 80. Another visor — or browser tab —
// fetchDmsg(thisPK, "/") to read it. No domain, no IP, no CA: a serverless,
// in-browser site reachable by public key over the dmsg overlay (while the tab
// stays open). Combined with fetchDmsg, a tab can host its own content AND browse
// others' — a virtual browser + self-hosting in one.
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"syscall/js"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// contentPort is the DEFAULT dmsg port the self-host HTTP server listens on
// (matches fetchDmsg's default host port); serveContent(map, port) overrides it.
const contentPort uint16 = 80

type contentEntry struct {
	ct      string
	body    []byte
	enabled bool // false → served as 404 (disabled but retained, so it can be re-enabled)
}

var (
	contentMu sync.RWMutex
	// contentByPort holds per-dmsg-port content maps (path → entry), so a tab can
	// host different sites on different ports; servingPorts tracks which ports
	// already have an accept loop running.
	contentByPort = map[uint16]map[string]contentEntry{}
	servingPorts  = map[uint16]bool{}
)

// jsServeContent(map[, port]) registers content to self-host over dmsg on the
// given port (default 80) and starts that port's HTTP server once. map is
// { "/": {ct:"text/html", body:"<html>…"}, "/img.png": {ct:"image/png",
// body:"<base64>", b64:true}, … } — set b64:true when body is base64 (binary /
// uploaded files). Call again to add/replace paths (same or different port).
func jsServeContent(_ js.Value, args []js.Value) interface{} {
	if len(args) == 0 || !args[0].Truthy() {
		return js.Global().Get("Error").New("serveContent: expected a {path: {ct, body}} map")
	}
	m := args[0]
	port := contentPort
	if len(args) > 1 && args[1].Truthy() {
		if p := args[1].Int(); p > 0 && p < 65536 {
			port = uint16(p)
		}
	}
	keys := js.Global().Get("Object").Call("keys", m)
	contentMu.Lock()
	cm := contentByPort[port]
	if cm == nil {
		cm = map[string]contentEntry{}
		contentByPort[port] = cm
	}
	for i := 0; i < keys.Length(); i++ {
		p := keys.Index(i).String()
		e := m.Get(p)
		body := []byte(e.Get("body").String())
		if e.Get("b64").Truthy() {
			if dec, err := base64.StdEncoding.DecodeString(string(body)); err == nil {
				body = dec
			}
		}
		cm[p] = contentEntry{ct: e.Get("ct").String(), body: body, enabled: true}
	}
	start := !servingPorts[port]
	servingPorts[port] = true
	contentMu.Unlock()

	if start {
		go serveContentOverDmsg(port)
	}
	return js.ValueOf(keys.Length())
}

func serveContentOverDmsg(port uint16) {
	if dmsgC == nil {
		vlog("serveContent: not booted")
		return
	}
	lis, err := dmsgC.Listen(port)
	if err != nil {
		vlog(fmt.Sprintf("serveContent: listen dmsg:%d: %s", port, err.Error()))
		return
	}
	vlog(fmt.Sprintf("serveContent: hosting on dmsg:%d (reach via fetchDmsg(<this-pk>:%d, ...))", port, port))
	for {
		str, err := lis.AcceptStream()
		if err != nil {
			return
		}
		go handleContentStream(str, port)
	}
}

// handleContentStream serves one HTTP/1.1 request over a dmsg stream: read the
// request line + headers, look the path up in the content map, write a minimal
// response. Static content only — enough to host a page, not a web framework.
func handleContentStream(s *dmsg.Stream, port uint16) {
	defer s.Close() //nolint:errcheck
	br := bufio.NewReader(s)
	line, err := br.ReadString('\n') // "GET /path HTTP/1.1\r\n"
	if err != nil {
		return
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return
	}
	path := fields[1]
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	// Drain request headers (we don't use them).
	for {
		h, err := br.ReadString('\n')
		if err != nil || h == "\r\n" || h == "\n" {
			break
		}
	}

	// Dynamic endpoints (e.g. /health, /ping on port 80) take priority over the
	// static map, so peer parity endpoints can't be shadowed by uploaded content.
	// dynamicHandler is nil until startPeerServices installs it (peerserve_js.go).
	if dynamicHandler != nil {
		if ct, body, handled := dynamicHandler(port, path); handled {
			_, _ = fmt.Fprintf(s, "HTTP/1.1 200 OK\r\nContent-Type: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", ct, len(body)) //nolint:errcheck
			_, _ = s.Write(body)                                                                                                           //nolint:errcheck
			return
		}
	}

	contentMu.RLock()
	e, ok := contentByPort[port][path]
	contentMu.RUnlock()
	if !ok || !e.enabled { // absent OR disabled by the operator → 404
		_, _ = io.WriteString(s, "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n") //nolint:errcheck
		return
	}
	_, _ = fmt.Fprintf(s, "HTTP/1.1 200 OK\r\nContent-Type: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", e.ct, len(e.body)) //nolint:errcheck
	_, _ = s.Write(e.body)                                                                                                             //nolint:errcheck
}

// dynamicHandler, when set, is consulted before the static content map in
// handleContentStream. It returns (contentType, body, handled=true) to serve a
// computed response (e.g. /health JSON, /ping pong). Installed by
// startPeerServices; nil otherwise. Read on the accept goroutine, set once at
// boot before the port-80 server starts, so no lock is needed.
var dynamicHandler func(port uint16, path string) (string, []byte, bool)

// jsHostedContent() → JSON array of every hosted entry across all ports:
// [{port, path, ct, size, enabled}], sorted by port then path. Powers the host
// window's "what am I hosting" list.
func jsHostedContent(js.Value, []js.Value) interface{} {
	type row struct {
		Port    uint16 `json:"port"`
		Path    string `json:"path"`
		CT      string `json:"ct"`
		Size    int    `json:"size"`
		Enabled bool   `json:"enabled"`
	}
	contentMu.RLock()
	var rows []row
	for port, cm := range contentByPort {
		for p, e := range cm {
			rows = append(rows, row{Port: port, Path: p, CT: e.ct, Size: len(e.body), Enabled: e.enabled})
		}
	}
	contentMu.RUnlock()
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Port != rows[j].Port {
			return rows[i].Port < rows[j].Port
		}
		return rows[i].Path < rows[j].Path
	})
	b, _ := json.Marshal(rows) //nolint:errcheck
	return string(b)
}

// jsUnserveContent(path[, port]) removes a hosted path entirely. Returns true if
// it existed and was removed.
func jsUnserveContent(_ js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return false
	}
	path := args[0].String()
	port := hostArgPort(args, 1)
	contentMu.Lock()
	defer contentMu.Unlock()
	if cm := contentByPort[port]; cm != nil {
		if _, ok := cm[path]; ok {
			delete(cm, path)
			return true
		}
	}
	return false
}

// jsSetContentEnabled(path, enabled[, port]) toggles whether a hosted path is
// served (disabled → 404) WITHOUT discarding its body, so it can be re-enabled.
// Returns the new enabled state.
func jsSetContentEnabled(_ js.Value, args []js.Value) interface{} {
	if len(args) < 2 {
		return false
	}
	path := args[0].String()
	enabled := args[1].Truthy()
	port := hostArgPort(args, 2)
	contentMu.Lock()
	defer contentMu.Unlock()
	if cm := contentByPort[port]; cm != nil {
		if e, ok := cm[path]; ok {
			e.enabled = enabled
			cm[path] = e
			return enabled
		}
	}
	return false
}

// hostArgPort reads an optional port from args[idx], defaulting to contentPort.
func hostArgPort(args []js.Value, idx int) uint16 {
	if len(args) > idx && args[idx].Truthy() {
		if p := args[idx].Int(); p > 0 && p < 65536 {
			return uint16(p)
		}
	}
	return contentPort
}
