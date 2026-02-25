//go:build js && wasm

package main

import (
	"syscall/js"

	"github.com/skycoin/skywire/pkg/tpviz/wasm/ui"
)

func main() {
	console := js.Global().Get("console")
	console.Call("log", "[tpviz] WASM module loaded")

	// Wait for DOM to be ready
	done := make(chan struct{})

	// Create the app when the page loads
	js.Global().Set("initTpviz", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		console.Call("log", "[tpviz] initTpviz called")
		canvasID := "graph-canvas"
		if len(args) > 0 {
			canvasID = args[0].String()
		}

		app := ui.NewApp(canvasID)
		if app == nil {
			console.Call("error", "Failed to create app - canvas not found")
			return nil
		}

		console.Call("log", "[tpviz] App created, starting LoadData")
		// Load data and start the render loop
		go app.LoadData()
		app.Run()

		return nil
	}))

	// Keep the Go program running
	<-done
}
