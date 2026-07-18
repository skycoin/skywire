//go:build js && wasm

// Package main cmd/skywire/commands/web/wasm/main.go c4-vis-cli
//
// Renders a single shell-like prompt: `skywire $ <input>`. As the
// operator types, the WASM client parses the line into command path
// + flags + args, looks up the matching cobra node in /api/tree,
// and shows its help (Short, Long, Flags, Example) below the input.
// Pressing Enter executes the line via POST /api/run + SSE stream.
// Tab autocompletes the current token (subcommand or flag name).
// Up/Down recall previous lines.
//
// All DOM manipulation via syscall/js — no JS framework, no
// client-side state outside this module.
//
// Build: tinygo build -target wasm -no-debug -o ../static/b.wasm .
package main

import (
	"sort"
	"strings"
	"syscall/js"
)

var (
	tree    map[string]node
	history []string
	histIdx int // -1 = current input
)

func main() {
	d := js.Global().Get("document")
	if d.Get("readyState").String() == "loading" {
		var ready js.Func
		ready = js.FuncOf(func(_ js.Value, _ []js.Value) interface{} {
			boot()
			ready.Release()
			return nil
		})
		d.Call("addEventListener", "DOMContentLoaded", ready)
	} else {
		boot()
	}
	select {}
}

func boot() {
	loadTree()
}

type node struct {
	Path     string
	Name     string
	Short    string
	Long     string
	Example  string
	Children []string
	Flags    []flag
	Runnable bool
}

type flag struct {
	Name      string
	Shorthand string
	Type      string
	Default   string
	Usage     string
}

func loadTree() {
	then := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		args[0].Call("text").Call("then", js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
			tree = parseTree(args[0].String())
			renderShell()
			update("")
			return nil
		}))
		return nil
	})
	js.Global().Call("fetch", "/api/tree").Call("then", then)
}

// renderShell paints the single-column shell layout: prompt input
// on top, help/completion panel mid, output panel bottom. Input
// focus is captured on load + every click anywhere in the page.
func renderShell() {
	d := js.Global().Get("document")
	app := d.Call("getElementById", "app")
	if app.IsUndefined() || app.IsNull() {
		d.Get("body").Set("innerHTML", `<pre style="color:red">missing #app in index.html</pre>`)
		return
	}
	app.Set("innerHTML", `
<div class="sh">
  <div class="sh-prompt-row">
    <span class="sh-prompt">skywire $</span>
    <input id="sh-input" class="sh-input" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false" autofocus>
  </div>
  <div id="sh-completions" class="sh-completions"></div>
  <div id="sh-help" class="sh-help"></div>
  <div id="sh-output-wrap" hidden>
    <div class="sh-output-header">
      <span id="sh-output-label"></span>
      <button id="sh-cancel" type="button" hidden>cancel</button>
    </div>
    <pre id="sh-output" class="sh-output"></pre>
  </div>
</div>`)

	input := d.Call("getElementById", "sh-input")
	input.Call("focus")

	// Refocus on any click outside an interactive element. Keeps the
	// keyboard captured for the prompt without explicit Ctrl+L style
	// gestures.
	bodyClick := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		target := args[0].Get("target")
		tag := target.Get("tagName").String()
		if tag != "BUTTON" && tag != "A" {
			input.Call("focus")
		}
		return nil
	})
	d.Get("body").Call("addEventListener", "click", bodyClick)

	inputCb := js.FuncOf(func(_ js.Value, _ []js.Value) interface{} {
		update(input.Get("value").String())
		return nil
	})
	input.Call("addEventListener", "input", inputCb)

	keyCb := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		ev := args[0]
		key := ev.Get("key").String()
		switch key {
		case "Enter":
			ev.Call("preventDefault")
			line := strings.TrimSpace(input.Get("value").String())
			if line == "" {
				return nil
			}
			history = append(history, line)
			histIdx = -1
			runLine(line)
			input.Set("value", "")
			update("")
		case "Tab":
			ev.Call("preventDefault")
			completed := autocomplete(input.Get("value").String())
			if completed != "" {
				input.Set("value", completed)
				update(completed)
			}
		case "ArrowUp":
			if len(history) == 0 {
				return nil
			}
			ev.Call("preventDefault")
			if histIdx == -1 {
				histIdx = len(history)
			}
			if histIdx > 0 {
				histIdx--
			}
			input.Set("value", history[histIdx])
			update(history[histIdx])
		case "ArrowDown":
			if histIdx == -1 {
				return nil
			}
			ev.Call("preventDefault")
			histIdx++
			if histIdx >= len(history) {
				histIdx = -1
				input.Set("value", "")
				update("")
			} else {
				input.Set("value", history[histIdx])
				update(history[histIdx])
			}
		}
		return nil
	})
	input.Call("addEventListener", "keydown", keyCb)
}

// update re-renders the help + completions panel based on the
// current input line. Called on every keystroke and on history
// navigation.
func update(line string) {
	d := js.Global().Get("document")

	path, finishedTokens, lastToken := resolvePath(line)
	n, ok := tree[path]
	if !ok {
		n = tree[""]
		path = ""
	}

	// Help panel: name + Short + Long + Example + Flags table.
	help := d.Call("getElementById", "sh-help")
	help.Set("innerHTML", "")

	header := d.Call("createElement", "div")
	header.Set("className", "sh-help-header")
	cmdSoFar := "skywire"
	if path != "" {
		cmdSoFar += " " + strings.ReplaceAll(path, ".", " ")
	}
	header.Set("textContent", cmdSoFar)
	help.Call("appendChild", header)

	if n.Short != "" {
		s := d.Call("createElement", "div")
		s.Set("className", "sh-help-short")
		s.Set("textContent", n.Short)
		help.Call("appendChild", s)
	}
	if n.Long != "" {
		pre := d.Call("createElement", "pre")
		pre.Set("className", "sh-help-long")
		pre.Set("textContent", n.Long)
		help.Call("appendChild", pre)
	}
	if n.Runnable && len(n.Flags) > 0 {
		flagsH := d.Call("createElement", "div")
		flagsH.Set("className", "sh-help-section")
		flagsH.Set("textContent", "Flags")
		help.Call("appendChild", flagsH)
		table := d.Call("createElement", "table")
		table.Set("className", "sh-flags")
		for _, fl := range n.Flags {
			tr := d.Call("createElement", "tr")
			tdName := d.Call("createElement", "td")
			tdName.Set("className", "sh-flag-name")
			name := "--" + fl.Name
			if fl.Shorthand != "" {
				name = "-" + fl.Shorthand + ", " + name
			}
			tdName.Set("textContent", name)
			tdType := d.Call("createElement", "td")
			tdType.Set("className", "sh-flag-type")
			tdType.Set("textContent", fl.Type)
			tdUsage := d.Call("createElement", "td")
			tdUsage.Set("className", "sh-flag-usage")
			usage := fl.Usage
			if fl.Default != "" && fl.Default != "false" && fl.Default != "[]" {
				usage += " (default " + fl.Default + ")"
			}
			tdUsage.Set("textContent", usage)
			tr.Call("appendChild", tdName)
			tr.Call("appendChild", tdType)
			tr.Call("appendChild", tdUsage)
			table.Call("appendChild", tr)
		}
		help.Call("appendChild", table)
	}
	if n.Example != "" {
		eh := d.Call("createElement", "div")
		eh.Set("className", "sh-help-section")
		eh.Set("textContent", "Examples")
		help.Call("appendChild", eh)
		pre := d.Call("createElement", "pre")
		pre.Set("className", "sh-help-example")
		pre.Set("textContent", n.Example)
		help.Call("appendChild", pre)
	}

	// Completions panel: list of subcommands (if non-leaf) or flag
	// suggestions (if last token starts with "-").
	compPanel := d.Call("getElementById", "sh-completions")
	compPanel.Set("innerHTML", "")
	suggestions := []string{}
	if strings.HasPrefix(lastToken, "-") && !finishedTokens {
		// Flag completion at the current path.
		flagPrefix := strings.TrimLeft(lastToken, "-")
		for _, fl := range n.Flags {
			if flagPrefix == "" || strings.HasPrefix(fl.Name, flagPrefix) {
				suggestions = append(suggestions, "--"+fl.Name)
			}
		}
	} else {
		// Subcommand completion.
		for _, childPath := range n.Children {
			c := tree[childPath]
			if lastToken == "" || strings.HasPrefix(c.Name, lastToken) {
				suggestions = append(suggestions, c.Name)
			}
		}
	}
	sort.Strings(suggestions)
	if len(suggestions) > 0 {
		hdr := d.Call("createElement", "div")
		hdr.Set("className", "sh-comp-header")
		hdr.Set("textContent", "↹ Tab — completions")
		compPanel.Call("appendChild", hdr)
		row := d.Call("createElement", "div")
		row.Set("className", "sh-comp-row")
		for _, s := range suggestions {
			pill := d.Call("createElement", "span")
			pill.Set("className", "sh-comp-pill")
			pill.Set("textContent", s)
			row.Call("appendChild", pill)
		}
		compPanel.Call("appendChild", row)
	}
}

// resolvePath walks the input line token-by-token, advancing through
// the cobra tree until a token isn't a known subcommand name. Returns
// the dot-path matched, whether the last token is "finished" (a
// trailing space means yes), and the last (possibly partial) token
// for completion logic.
func resolvePath(line string) (string, bool, string) {
	finished := strings.HasSuffix(line, " ")
	tokens := strings.Fields(line)
	path := ""
	consumed := 0
	for i, t := range tokens {
		if strings.HasPrefix(t, "-") {
			// Hit a flag — stop walking the path.
			break
		}
		candidate := path
		if candidate != "" {
			candidate += "."
		}
		candidate += t
		if _, ok := tree[candidate]; !ok {
			break
		}
		path = candidate
		consumed = i + 1
	}
	lastToken := ""
	if !finished && len(tokens) > 0 {
		lastToken = tokens[len(tokens)-1]
		if consumed == len(tokens) {
			// Last token was a complete subcommand → empty partial.
			lastToken = ""
		}
	}
	return path, finished, lastToken
}

// autocomplete returns the input line with the last token expanded
// to the unique completion, or empty if no unique match. Common
// prefix expansion isn't done — yet.
func autocomplete(line string) string {
	path, finished, lastToken := resolvePath(line)
	n, ok := tree[path]
	if !ok {
		return ""
	}

	var candidates []string
	isFlag := strings.HasPrefix(lastToken, "-")
	if isFlag {
		prefix := strings.TrimLeft(lastToken, "-")
		for _, fl := range n.Flags {
			if strings.HasPrefix(fl.Name, prefix) {
				candidates = append(candidates, "--"+fl.Name)
			}
		}
	} else if !finished {
		for _, childPath := range n.Children {
			c := tree[childPath]
			if strings.HasPrefix(c.Name, lastToken) {
				candidates = append(candidates, c.Name)
			}
		}
	}
	if len(candidates) == 0 {
		// Trailing space: list children of current path → noop here,
		// the completions panel shows them; tab doesn't change input.
		return ""
	}
	if len(candidates) == 1 {
		// Replace the last token with the unique candidate + space.
		fields := strings.Fields(line)
		if !finished && len(fields) > 0 {
			fields = fields[:len(fields)-1]
		}
		fields = append(fields, candidates[0])
		return strings.Join(fields, " ") + " "
	}
	// Multi-candidate: extend to common prefix.
	common := candidates[0]
	for _, c := range candidates[1:] {
		common = commonPrefix(common, c)
	}
	if len(common) > len(lastToken) {
		fields := strings.Fields(line)
		if !finished && len(fields) > 0 {
			fields = fields[:len(fields)-1]
		}
		fields = append(fields, common)
		return strings.Join(fields, " ")
	}
	return ""
}

func commonPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return a[:i]
}

// runLine parses a full input line into command path + flags + args
// and POSTs to /api/run. Output streams via SSE into #sh-output.
func runLine(line string) {
	path, flags, args := parseLine(line)
	if _, ok := tree[path]; !ok {
		appendOutput("error: unknown command\n")
		return
	}

	body := `{"path":` + jsonString(path)
	if len(flags) > 0 {
		body += `,"flags":{`
		first := true
		for k, v := range flags {
			if !first {
				body += ","
			}
			body += jsonString(k) + ":" + jsonString(v)
			first = false
		}
		body += `}`
	}
	if len(args) > 0 {
		body += `,"args":[`
		for i, a := range args {
			if i > 0 {
				body += ","
			}
			body += jsonString(a)
		}
		body += `]`
	}
	body += `}`

	d := js.Global().Get("document")
	out := d.Call("getElementById", "sh-output")
	wrap := d.Call("getElementById", "sh-output-wrap")
	wrap.Set("hidden", false)
	label := d.Call("getElementById", "sh-output-label")
	label.Set("textContent", "$ skywire "+strings.ReplaceAll(path, ".", " "))
	// Append history separator instead of clearing — operator
	// expects a transcript view, not a single-shot REPL.
	if cur := out.Get("textContent").String(); cur != "" {
		out.Set("textContent", cur+"\n")
	}
	out.Set("textContent", out.Get("textContent").String()+"$ "+line+"\n")

	cancelBtn := d.Call("getElementById", "sh-cancel")
	cancelBtn.Set("hidden", true)

	headers := js.Global().Get("Object").New()
	headers.Set("Content-Type", "application/json")
	opts := js.Global().Get("Object").New()
	opts.Set("method", "POST")
	opts.Set("body", body)
	opts.Set("headers", headers)
	then := js.FuncOf(func(_ js.Value, fetchArgs []js.Value) interface{} {
		fetchArgs[0].Call("json").Call("then", js.FuncOf(func(_ js.Value, jsonArgs []js.Value) interface{} {
			id := jsonArgs[0].Get("id").String()
			subscribe(id)
			return nil
		}))
		return nil
	})
	js.Global().Call("fetch", "/api/run", opts).Call("then", then)
}

// parseLine breaks a typed line into (command-path, flag map,
// positional args). Tokens with --name or --name=value are flags;
// the next-token-after-flag-name (when no =) is the flag's value
// unless it itself starts with --. Bool flags accept --name or
// --name=true. Single-dash shorthand isn't handled yet (operator
// types --long-name for now).
func parseLine(line string) (string, map[string]string, []string) {
	tokens := strings.Fields(line)
	// Find path prefix.
	path := ""
	i := 0
	for ; i < len(tokens); i++ {
		t := tokens[i]
		if strings.HasPrefix(t, "-") {
			break
		}
		candidate := path
		if candidate != "" {
			candidate += "."
		}
		candidate += t
		if _, ok := tree[candidate]; !ok {
			break
		}
		path = candidate
	}
	flags := map[string]string{}
	args := []string{}
	for i < len(tokens) {
		t := tokens[i]
		if strings.HasPrefix(t, "--") {
			name := strings.TrimPrefix(t, "--")
			if eq := strings.Index(name, "="); eq >= 0 {
				flags[name[:eq]] = name[eq+1:]
				i++
				continue
			}
			// Bool detection — if the flag's typed-tree type is bool
			// and the next token starts with -- (or doesn't exist),
			// treat as standalone --flag (true).
			if isBoolFlag(path, name) {
				if i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "-") {
					// Operator wrote --bool value — accept the value.
					flags[name] = tokens[i+1]
					i += 2
				} else {
					flags[name] = "true"
					i++
				}
				continue
			}
			if i+1 < len(tokens) {
				flags[name] = tokens[i+1]
				i += 2
			} else {
				flags[name] = ""
				i++
			}
			continue
		}
		args = append(args, t)
		i++
	}
	return path, flags, args
}

func isBoolFlag(path, flagName string) bool {
	n, ok := tree[path]
	if !ok {
		return false
	}
	for _, fl := range n.Flags {
		if fl.Name == flagName {
			return fl.Type == "bool"
		}
	}
	return false
}

func subscribe(id string) {
	d := js.Global().Get("document")
	out := d.Call("getElementById", "sh-output")
	cancelBtn := d.Call("getElementById", "sh-cancel")
	cancelBtn.Set("hidden", false)

	es := js.Global().Get("EventSource").New("/api/sse/" + id)
	stdout := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		data := args[0].Get("data").String()
		out.Set("textContent", out.Get("textContent").String()+data+"\n")
		out.Set("scrollTop", out.Get("scrollHeight"))
		return nil
	})
	exitCB := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		code := args[0].Get("data").String()
		out.Set("textContent", out.Get("textContent").String()+"[exit "+code+"]\n")
		es.Call("close")
		cancelBtn.Set("hidden", true)
		return nil
	})
	es.Call("addEventListener", "stdout", stdout)
	es.Call("addEventListener", "exit", exitCB)

	cancelClick := js.FuncOf(func(_ js.Value, _ []js.Value) interface{} {
		opts := js.Global().Get("Object").New()
		opts.Set("method", "POST")
		js.Global().Call("fetch", "/api/cancel/"+id, opts)
		return nil
	})
	cancelBtn.Call("addEventListener", "click", cancelClick)
}

func appendOutput(s string) {
	out := js.Global().Get("document").Call("getElementById", "sh-output")
	wrap := js.Global().Get("document").Call("getElementById", "sh-output-wrap")
	wrap.Set("hidden", false)
	out.Set("textContent", out.Get("textContent").String()+s)
}

// jsonString escapes s for inclusion in a JSON document. Hand-rolled
// to avoid encoding/json's reflect dependency.
func jsonString(s string) string {
	b := strings.Builder{}
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString("\\u00")
				const hex = "0123456789abcdef"
				b.WriteByte(hex[r>>4])
				b.WriteByte(hex[r&0xF])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// parseTree hand-rolled JSON decoder — see equivalent in the
// install-page generator's WASM client. Doesn't use encoding/json
// (drags reflect) and we don't need a general decoder here.
func parseTree(s string) map[string]node {
	p := &parser{s: s}
	out := map[string]node{}
	p.expect('{')
	for {
		p.ws()
		if p.peek() == '}' {
			p.next()
			break
		}
		k := p.parseString()
		p.ws()
		p.expect(':')
		v := p.parseNode()
		out[k] = v
		p.ws()
		if p.peek() == ',' {
			p.next()
			continue
		}
	}
	return out
}

type parser struct {
	s string
	i int
}

func (p *parser) ws() {
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			p.i++
			continue
		}
		break
	}
}

func (p *parser) peek() byte {
	if p.i >= len(p.s) {
		return 0
	}
	return p.s[p.i]
}

func (p *parser) next() byte {
	c := p.peek()
	p.i++
	return c
}

func (p *parser) expect(c byte) {
	p.ws()
	if p.peek() != c {
		return
	}
	p.next()
}

func (p *parser) parseString() string {
	p.ws()
	if p.peek() != '"' {
		return ""
	}
	p.next()
	b := strings.Builder{}
	for p.i < len(p.s) {
		c := p.next()
		if c == '"' {
			return b.String()
		}
		if c == '\\' {
			esc := p.next()
			switch esc {
			case '"', '\\', '/':
				b.WriteByte(esc)
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'u':
				if p.i+4 > len(p.s) {
					return b.String()
				}
				h := p.s[p.i : p.i+4]
				p.i += 4
				r := 0
				for _, c := range h {
					r <<= 4
					switch {
					case c >= '0' && c <= '9':
						r |= int(c - '0')
					case c >= 'a' && c <= 'f':
						r |= int(c-'a') + 10
					case c >= 'A' && c <= 'F':
						r |= int(c-'A') + 10
					}
				}
				b.WriteRune(rune(r))
			default:
				b.WriteByte(esc)
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func (p *parser) parseBool() bool {
	p.ws()
	if p.i+4 <= len(p.s) && p.s[p.i:p.i+4] == "true" {
		p.i += 4
		return true
	}
	if p.i+5 <= len(p.s) && p.s[p.i:p.i+5] == "false" {
		p.i += 5
	}
	return false
}

func (p *parser) skipValue() {
	p.ws()
	c := p.peek()
	switch c {
	case '"':
		p.parseString()
	case '{', '[':
		open := c
		closeB := byte('}')
		if open == '[' {
			closeB = ']'
		}
		depth := 1
		p.next()
		for p.i < len(p.s) && depth > 0 {
			c = p.next()
			if c == '"' {
				p.i--
				p.parseString()
				continue
			}
			if c == open {
				depth++
			} else if c == closeB {
				depth--
			}
		}
	default:
		for p.i < len(p.s) {
			c = p.peek()
			if c == ',' || c == '}' || c == ']' || c == ' ' || c == '\n' || c == '\t' || c == '\r' {
				return
			}
			p.next()
		}
	}
}

func (p *parser) parseStringArray() []string {
	out := []string{}
	p.ws()
	if p.peek() != '[' {
		return out
	}
	p.next()
	for {
		p.ws()
		if p.peek() == ']' {
			p.next()
			return out
		}
		out = append(out, p.parseString())
		p.ws()
		if p.peek() == ',' {
			p.next()
			continue
		}
	}
}

func (p *parser) parseFlags() []flag {
	out := []flag{}
	p.ws()
	if p.peek() != '[' {
		return out
	}
	p.next()
	for {
		p.ws()
		if p.peek() == ']' {
			p.next()
			return out
		}
		f := flag{}
		p.expect('{')
		for {
			p.ws()
			if p.peek() == '}' {
				p.next()
				break
			}
			k := p.parseString()
			p.ws()
			p.expect(':')
			switch k {
			case "name":
				f.Name = p.parseString()
			case "shorthand":
				f.Shorthand = p.parseString()
			case "type":
				f.Type = p.parseString()
			case "default":
				f.Default = p.parseString()
			case "usage":
				f.Usage = p.parseString()
			default:
				p.skipValue()
			}
			p.ws()
			if p.peek() == ',' {
				p.next()
			}
		}
		out = append(out, f)
		p.ws()
		if p.peek() == ',' {
			p.next()
		}
	}
}

func (p *parser) parseNode() node {
	n := node{}
	p.ws()
	p.expect('{')
	for {
		p.ws()
		if p.peek() == '}' {
			p.next()
			return n
		}
		k := p.parseString()
		p.ws()
		p.expect(':')
		switch k {
		case "path":
			n.Path = p.parseString()
		case "name":
			n.Name = p.parseString()
		case "short":
			n.Short = p.parseString()
		case "long":
			n.Long = p.parseString()
		case "example":
			n.Example = p.parseString()
		case "children":
			n.Children = p.parseStringArray()
		case "flags":
			n.Flags = p.parseFlags()
		case "runnable":
			n.Runnable = p.parseBool()
		default:
			p.skipValue()
		}
		p.ws()
		if p.peek() == ',' {
			p.next()
		}
	}
}
