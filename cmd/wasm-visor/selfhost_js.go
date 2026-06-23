//go:build js && wasm

// Package main — in-browser self-hosting: serve content over dmsg from the tab.
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
	"fmt"
	"io"
	"strings"
	"sync"
	"syscall/js"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// contentPort is the dmsg port the self-host HTTP server listens on (matches
// fetchDmsg's default host port).
const contentPort uint16 = 80

type contentEntry struct {
	ct   string
	body []byte
}

var (
	contentMu      sync.RWMutex
	content        = map[string]contentEntry{}
	contentServing bool
)

// jsServeContent(map) registers content to self-host over dmsg and starts the
// dmsg:80 HTTP server once. map is { "/": {ct:"text/html", body:"<html>…"},
// "/style.css": {ct:"text/css", body:"…"}, … }. Call again to add/replace paths.
func jsServeContent(_ js.Value, args []js.Value) interface{} {
	if len(args) == 0 || !args[0].Truthy() {
		return js.Global().Get("Error").New("serveContent: expected a {path: {ct, body}} map")
	}
	m := args[0]
	keys := js.Global().Get("Object").Call("keys", m)
	contentMu.Lock()
	for i := 0; i < keys.Length(); i++ {
		p := keys.Index(i).String()
		e := m.Get(p)
		content[p] = contentEntry{ct: e.Get("ct").String(), body: []byte(e.Get("body").String())}
	}
	start := !contentServing
	contentServing = true
	contentMu.Unlock()

	if start {
		go serveContentOverDmsg()
	}
	return js.ValueOf(keys.Length())
}

func serveContentOverDmsg() {
	if dmsgC == nil {
		vlog("serveContent: not booted")
		return
	}
	lis, err := dmsgC.Listen(contentPort)
	if err != nil {
		vlog("serveContent: listen dmsg:80: " + err.Error())
		return
	}
	vlog(fmt.Sprintf("serveContent: hosting on dmsg:%d (reach via fetchDmsg(<this-pk>, ...))", contentPort))
	for {
		str, err := lis.AcceptStream()
		if err != nil {
			return
		}
		go handleContentStream(str)
	}
}

// handleContentStream serves one HTTP/1.1 request over a dmsg stream: read the
// request line + headers, look the path up in the content map, write a minimal
// response. Static content only — enough to host a page, not a web framework.
func handleContentStream(s *dmsg.Stream) {
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

	contentMu.RLock()
	e, ok := content[path]
	contentMu.RUnlock()
	if !ok {
		_, _ = io.WriteString(s, "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n") //nolint:errcheck
		return
	}
	_, _ = fmt.Fprintf(s, "HTTP/1.1 200 OK\r\nContent-Type: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", e.ct, len(e.body)) //nolint:errcheck
	_, _ = s.Write(e.body) //nolint:errcheck
}
