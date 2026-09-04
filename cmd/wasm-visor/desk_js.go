//go:build js && wasm

// cmd/wasm-visor/desk_js.go — the desk surface, from its promoted home.
//
// The desk chrome and window registry come from github.com/0magnet/desk (the
// panel, the Applications menu, task buttons, launch/teardown); this file only
// registers skywire's panes — the websh terminal, the netscrape browser, the
// jsfs file manager, and the hypervisor dashboard — and publishes the small
// façade desk-boot.js drives (globalThis.__skywireDesk with the
// openConsole/openWindow contract the desk pages already use). Nothing
// chrome-shaped is hand-rolled here: skywire brings panes, the library brings
// the desk.
package main

import (
	"strings"
	"syscall/js"

	"github.com/0magnet/desk"
	"github.com/0magnet/desk/panes/files"
	"github.com/0magnet/netscrape"
)

// funcPane adapts a mount function to desk.Pane for surfaces that own their
// lifetime through their DOM handlers (the shell, the browser).
type funcPane struct{ mount func(el js.Value) error }

func (p funcPane) Mount(el js.Value) error { return p.mount(el) }
func (funcPane) Close()                    {}

// installDesk registers skywire's desk apps and mounts the library panel.
// Call from a DOM-bearing role after installShell/installBrowser.
func installDesk() {
	desk.Register(desk.App{
		Name: "terminal", Title: "terminal",
		Help:  "websh — the skywire shell",
		Width: 900, Height: 540,
		Open: func(args []string) (desk.Pane, error) {
			return funcPane{mount: func(el js.Value) error {
				h := jsOpenShell(js.Undefined(), []js.Value{el})
				// args[0] (optional): a command to run once the session is up.
				if len(args) > 0 && args[0] != "" {
					if hv, ok := h.(js.Value); ok && hv.Truthy() && hv.Get("run").Type() == js.TypeFunction {
						hv.Call("run", args[0])
					}
				}
				return nil
			}}, nil
		},
	})
	desk.Register(desk.App{
		Name: "browser", Title: "browser",
		Help:  "netscrape — browse the mesh and the clearnet",
		Width: 1000, Height: 640,
		Open: func(args []string) (desk.Pane, error) {
			return funcPane{mount: func(el js.Value) error {
				netscrape.Open(el)
				if len(args) > 0 && args[0] != "" {
					netscrape.Navigate(args[0])
				}
				return nil
			}}, nil
		},
	})
	desk.Register(desk.App{
		Name: "files", Title: "files",
		Help:  "the shared filesystem (jsfs)",
		Width: 760, Height: 500,
		Open: func(args []string) (desk.Pane, error) {
			dir := "/"
			if len(args) > 0 && args[0] != "" {
				dir = args[0]
			}
			return files.New(sharedShellFS(), dir), nil
		},
	})
	// The hypervisor UI is a browser TAB, not a window of its own — the desk
	// puts surfaces in tabs wherever the pane already has them, and the
	// dashboard is just a page. DirectLoader is what makes that tab render
	// NATIVELY: without it netscrape would transcode the Angular app into a
	// sandboxed srcdoc, which strips the same-origin it needs. Claim only
	// same-origin /vnet/ URLs — those are served by this page's own service
	// worker out of the visor's virtual loopback, so they are ours to render.
	netscrape.DirectLoader = func(u string) (string, bool) {
		loc := js.Global().Get("location")
		if !loc.Truthy() {
			return "", false
		}
		origin := loc.Get("origin").String()
		if origin == "" || !strings.HasPrefix(u, origin+"/vnet/") {
			return "", false
		}
		return u, true
	}

	desk.NewPanel()
	js.Global().Set("__skywireDesk", js.ValueOf(map[string]interface{}{
		"library": "0magnet/desk",
		"launch": js.FuncOf(func(_ js.Value, a []js.Value) any {
			if len(a) == 0 {
				return nil
			}
			args := make([]string, 0, len(a)-1)
			for _, v := range a[1:] {
				args = append(args, v.String())
			}
			_, err := desk.Launch(a[0].String(), args...)
			if err != nil {
				return err.Error()
			}
			return nil
		}),
		// openConsole({title, initCmd, bg}): a terminal window, optionally
		// running a first command; bg opens it minimized behind the rest.
		"openConsole": js.FuncOf(func(_ js.Value, a []js.Value) any {
			title, initCmd, bg := "terminal", "", false
			if len(a) > 0 && a[0].Type() == js.TypeObject {
				if v := a[0].Get("title"); v.Truthy() {
					title = v.String()
				}
				if v := a[0].Get("initCmd"); v.Truthy() {
					initCmd = v.String()
				}
				bg = a[0].Get("bg").Truthy()
			}
			w, err := desk.LaunchOpts("terminal", desk.Options{Title: title}, initCmd)
			if err != nil {
				return err.Error()
			}
			if bg && w != nil {
				w.Minimize()
			}
			return nil
		}),
		// openWindow([skipLanding], [url]): a browser window; the returned
		// handle carries the contract desk-boot drives (browseTo / openTab /
		// maximize). A url opens the window straight onto that page instead
		// of netscrape's built-in landing card.
		"openWindow": js.FuncOf(func(_ js.Value, a []js.Value) any {
			args := make([]string, 0, 1)
			if len(a) >= 2 && a[1].Type() == js.TypeString && a[1].String() != "" {
				args = append(args, a[1].String())
			}
			w, err := desk.Launch("browser", args...)
			if err != nil {
				return nil
			}
			return js.ValueOf(map[string]interface{}{
				"browser": js.ValueOf(map[string]interface{}{
					"browseTo": js.FuncOf(func(_ js.Value, a []js.Value) any {
						if len(a) >= 2 {
							netscrape.Navigate("http://" + a[0].String() + a[1].String())
						}
						return nil
					}),
				}),
				"openTab": js.FuncOf(func(_ js.Value, a []js.Value) any {
					if len(a) >= 2 {
						scheme := "http"
						if len(a) >= 3 && a[2].Truthy() {
							scheme = a[2].String()
						}
						bg := len(a) >= 4 && a[3].Truthy()
						netscrape.NewTab(scheme+"://"+a[0].String()+a[1].String(), bg)
					}
					return nil
				}),
				"wb": js.ValueOf(map[string]interface{}{
					"maximize": js.FuncOf(func(_ js.Value, _ []js.Value) any {
						if w != nil {
							w.Maximize()
						}
						return nil
					}),
				}),
			})
		}),
	}))
}
