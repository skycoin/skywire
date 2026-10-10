package progressive

import "encoding/base64"

// shipChunk is how many bytes of a widget one sequence carries: 4096 once
// in base64, as kitty's graphics protocol sends images.
const shipChunk = 3072

// Ship is the sequences that send a widget as content, for a program that is
// not in the host's tab — on another machine, over ssh — and so cannot offer
// one (widget.Register) and have it called. html is a whole document; the
// host runs it in a sandboxed iframe, with no access to the page, and gives
// it the same line to the program as any widget:
//
//	websh.send(value)       // reaches the program as a message event
//	websh.onmessage(f)      // f(value) for each progressive.Post
//
// Write every sequence, in order, then place the widget by name
// (Placement.Widget). A widget shipped by a command goes when it ends.
func Ship(name string, html []byte) []string {
	var seqs []string
	for len(html) > 0 || seqs == nil {
		n := min(len(html), shipChunk)
		meta := map[string]any{"kind": "html", "more": n < len(html)}
		seqs = append(seqs, osc("ship;"+name+";"+b64json(meta)+";"+base64.StdEncoding.EncodeToString(html[:n])))
		html = html[n:]
	}
	return seqs
}

// ShipWasm is the sequences sending a widget as a wasm module, built by Go
// or by TinyGo for js/wasm: the host runs it sandboxed as it runs a shipped
// document, with the loader its toolchain needs, and it draws in the
// document it is given (syscall/js) and talks over its line (package
// widget/inside). A program that ships the same widget wherever it runs —
// over ssh too — writes it once, in Go.
func ShipWasm(name string, module []byte) []string {
	var seqs []string
	for len(module) > 0 || seqs == nil {
		n := min(len(module), shipChunk)
		meta := map[string]any{"kind": "wasm", "more": n < len(module)}
		seqs = append(seqs, osc("ship;"+name+";"+b64json(meta)+";"+base64.StdEncoding.EncodeToString(module[:n])))
		module = module[n:]
	}
	return seqs
}
