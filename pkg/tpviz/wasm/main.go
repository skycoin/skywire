//go:build js && wasm

package main

import (
	"syscall/js"

	"github.com/skycoin/skywire/pkg/tpviz/wasm/ui"
)

func main() {
	// Wait for DOM to be ready
	done := make(chan struct{})

	// Create the app when the page loads
	js.Global().Set("initTpviz", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		canvasID := "graph-canvas"
		if len(args) > 0 {
			canvasID = args[0].String()
		}

		app := ui.NewApp(canvasID)
		if app == nil {
			js.Global().Get("console").Call("error", "Failed to create app - canvas not found")
			return nil
		}

		// Load data and start the render loop
		go app.LoadData()
		app.Run()

		return nil
	}))

	// Keep the Go program running
	<-done
}
