//go:build js && wasm

package main

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/skycoin/skywire/pkg/tpviz/wasm/ui"
)

func main() {
	// Create the app
	app := ui.NewApp(1400, 900)

	// Configure Ebitengine for browser
	ebiten.SetWindowSize(1400, 900)
	ebiten.SetWindowTitle("Transport Visualizer")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)

	// Load initial data
	go app.LoadData()

	// Run the game loop
	if err := ebiten.RunGame(app); err != nil {
		panic(err)
	}
}
