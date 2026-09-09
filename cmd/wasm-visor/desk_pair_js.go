//go:build js && wasm

// Package main cmd/wasm-visor/desk_pair_js.go c4-wasm-desk
//
// Two ☰ apps for a desk a visor serves (#4484 stage 5):
//
//   - pair: this tab's visor asks to drive the host as its hypervisor. The
//     window shows the tab's key and the fingerprint the operator sees in
//     `skywire cli visor hv pair`, whether the host has approved it, and takes
//     the one-time code from `hv pair --code` as the fallback. Both calls go
//     to the page's own origin (GET /pair/status, POST /pair), pre-auth.
//   - identity: export the tab's key (the persisted identity behind the
//     per-origin PK) as text to copy, or import one; a reload restarts the
//     visor under the imported key.
//
// The key never leaves the tab except as text the operator copies; the desk
// asks the CLI for it (`skywire cli config identity`), which reads the visor's
// own config file in the exec worker's filesystem.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall/js"
	"time"

	"github.com/0magnet/desk"
)

func registerPairingApps() {
	desk.Register(desk.App{
		Name: "pair", Title: "pair",
		Help:  "pair this tab with the visor serving it (hypervisor approval)",
		Width: 620, Height: 380,
		Open: func(_ []string) (desk.Pane, error) { return funcPane{mount: mountPairPane}, nil },
	})
	desk.Register(desk.App{
		Name: "identity", Title: "identity",
		Help:  "export or import this tab's key",
		Width: 680, Height: 460,
		Open: func(_ []string) (desk.Pane, error) { return funcPane{mount: mountIdentityPane}, nil },
	})
}

const paneCSS = "padding:14px 18px;font:14px/1.5 system-ui,sans-serif;color:#cdd2da;background:#15121d;height:100%;box-sizing:border-box;overflow:auto"

func paneEl(doc js.Value, tag, css, text string) js.Value {
	e := doc.Call("createElement", tag)
	if css != "" {
		e.Get("style").Set("cssText", css)
	}
	if text != "" {
		e.Set("textContent", text)
	}
	return e
}

func paneButton(doc js.Value, label string, onClick func()) js.Value {
	b := paneEl(doc, "button", "margin:4px 8px 4px 0;padding:6px 14px;font:inherit;cursor:pointer", label)
	b.Call("addEventListener", "click", js.FuncOf(func(js.Value, []js.Value) any {
		go onClick()
		return nil
	}))
	return b
}

// pageOrigin is the served page's origin, or "" when there is no page.
func pageOrigin() string {
	loc := js.Global().Get("location")
	if !loc.Truthy() {
		return ""
	}
	return loc.Get("origin").String()
}

// ownPK asks the tab's visor for its public key.
func ownPK(ctx context.Context) (string, error) {
	out, err := runHeadless(ctx, "skywire cli visor pk")
	if err != nil {
		return "", err
	}
	pk := strings.TrimSpace(out)
	if len(pk) != 66 {
		return "", fmt.Errorf("visor pk: %q", pk)
	}
	return pk, nil
}

type pairStatus struct {
	Fingerprint string `json:"fingerprint"`
	Paired      bool   `json:"paired"`
	Pending     bool   `json:"pending"`
}

func fetchPairStatus(ctx context.Context, origin, pk string) (pairStatus, error) {
	var st pairStatus
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/pair/status?pk="+pk, nil)
	if err != nil {
		return st, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return st, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512)) //nolint:errcheck
		return st, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return st, json.NewDecoder(resp.Body).Decode(&st)
}

func redeemPairCode(ctx context.Context, origin, pk, code string) error {
	body, _ := json.Marshal(map[string]string{"pk": pk, "code": code}) //nolint:errcheck
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/pair", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode == http.StatusForbidden {
		return errors.New("the host rejected that code")
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512)) //nolint:errcheck
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

func mountPairPane(el js.Value) error {
	doc := js.Global().Get("document")
	root := paneEl(doc, "div", paneCSS, "")
	el.Call("appendChild", root)
	origin := pageOrigin()
	if js.Global().Get("__SKYWIRE_LOCAL_PK__").Type() != js.TypeString || origin == "" {
		root.Call("appendChild", paneEl(doc, "p", "", "This desk is not served by a visor, so there is nothing to pair with."))
		return nil
	}
	hostPK := js.Global().Get("__SKYWIRE_LOCAL_PK__").String()
	root.Call("appendChild", paneEl(doc, "h3", "margin:0 0 8px", "Pair with the host"))
	root.Call("appendChild", paneEl(doc, "div", "font:12px monospace;word-break:break-all;color:#9aa0a6", "host "+hostPK))
	pkLine := paneEl(doc, "div", "font:12px monospace;word-break:break-all;margin-top:6px", "this tab: …")
	root.Call("appendChild", pkLine)
	status := paneEl(doc, "p", "margin:10px 0", "checking…")
	root.Call("appendChild", status)
	root.Call("appendChild", paneEl(doc, "p", "color:#9aa0a6;font-size:13px",
		"On the host: `skywire cli visor hv pair` lists this tab by fingerprint; `hv pair <fingerprint>` approves it. "+
			"Or make a code there with `hv pair --code` and enter it here."))
	code := paneEl(doc, "input", "font:16px monospace;letter-spacing:2px;padding:6px 8px;width:14em", "")
	code.Set("placeholder", "one-time code")
	code.Set("autocapitalize", "characters")
	row := paneEl(doc, "div", "display:flex;align-items:center;gap:8px;flex-wrap:wrap", "")
	row.Call("appendChild", code)
	result := paneEl(doc, "span", "", "")

	var pk string
	refresh := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if pk == "" {
			p, err := ownPK(ctx)
			if err != nil {
				status.Set("textContent", "cannot read this tab's key: "+err.Error())
				return
			}
			pk = p
			pkLine.Set("textContent", "this tab: "+pk)
		}
		st, err := fetchPairStatus(ctx, origin, pk)
		if err != nil {
			status.Set("textContent", "status unavailable: "+err.Error())
			return
		}
		switch {
		case st.Paired:
			status.Set("textContent", "Paired: the host accepts this tab as its hypervisor. Fingerprint "+st.Fingerprint+".")
		case st.Pending:
			status.Set("textContent", "Waiting for approval on the host. Fingerprint "+st.Fingerprint+".")
		default:
			status.Set("textContent", "Not paired. Fingerprint "+st.Fingerprint+" (the host lists it once this tab's transport is up).")
		}
	}
	row.Call("appendChild", paneButton(doc, "pair", func() {
		c := strings.TrimSpace(code.Get("value").String())
		if c == "" || pk == "" {
			result.Set("textContent", "enter the code first")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := redeemPairCode(ctx, origin, pk, c); err != nil {
			result.Set("textContent", err.Error())
			return
		}
		result.Set("textContent", "paired")
		code.Set("value", "")
		refresh()
	}))
	row.Call("appendChild", paneButton(doc, "refresh", refresh))
	row.Call("appendChild", result)
	root.Call("appendChild", row)
	go refresh()
	return nil
}

func mountIdentityPane(el js.Value) error {
	doc := js.Global().Get("document")
	root := paneEl(doc, "div", paneCSS, "")
	el.Call("appendChild", root)
	root.Call("appendChild", paneEl(doc, "h3", "margin:0 0 8px", "This tab's identity"))
	root.Call("appendChild", paneEl(doc, "p", "color:#9aa0a6;font-size:13px",
		"The key is what makes this tab the same visor every time this page is opened here. "+
			"Export it to carry the identity to another browser; import one to take that identity over here."))
	out := paneEl(doc, "textarea", "width:100%;height:90px;font:12px monospace;box-sizing:border-box", "")
	out.Set("readOnly", true)
	out.Set("placeholder", "press “show key”")
	msg := paneEl(doc, "div", "margin:6px 0;min-height:1.4em", "")
	root.Call("appendChild", paneButton(doc, "show key", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		text, err := runHeadless(ctx, "skywire cli config identity export")
		if err != nil {
			msg.Set("textContent", "export failed: "+err.Error())
			return
		}
		out.Set("value", strings.TrimSpace(text))
		msg.Set("textContent", "copy this somewhere safe; anyone holding it is this visor")
	}))
	root.Call("appendChild", out)
	root.Call("appendChild", msg)

	root.Call("appendChild", paneEl(doc, "h4", "margin:14px 0 6px", "Import a key"))
	in := paneEl(doc, "input", "width:100%;font:12px monospace;padding:6px 8px;box-sizing:border-box", "")
	in.Set("placeholder", "secret key (64 hex characters)")
	root.Call("appendChild", in)
	imsg := paneEl(doc, "div", "margin:6px 0;min-height:1.4em", "")
	row := paneEl(doc, "div", "", "")
	row.Call("appendChild", paneButton(doc, "import and reload", func() {
		sk := strings.TrimSpace(in.Get("value").String())
		if len(sk) != 64 {
			imsg.Set("textContent", "a secret key is 64 hex characters")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		text, err := runHeadless(ctx, "skywire cli config identity import --sk "+sk)
		if err != nil {
			imsg.Set("textContent", "import failed: "+err.Error()+" "+strings.TrimSpace(text))
			return
		}
		imsg.Set("textContent", "imported; reloading so the visor restarts under the new key…")
		time.Sleep(800 * time.Millisecond)
		js.Global().Get("location").Call("reload")
	}))
	row.Call("appendChild", imsg)
	root.Call("appendChild", row)
	return nil
}
