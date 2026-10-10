// Package progkit is a small widget kit for terminal programs on tcell v3
// that are progressive terminal programs (websh's PROTOCOL.md).
//
// Every widget draws itself in cells, so a program built on it runs in any
// terminal. Where the host offers placements and shipped widgets, an input,
// a button or a list is also laid over its cells as the real HTML element,
// which the person types into and clicks, and what they do comes back to the
// program as it would from the keyboard.
//
//	app, err := progkit.Open()
//	if err != nil { ... }
//	defer app.Close()
//	app.Run(func(f *progkit.Frame) { ... }, func(ev tcell.Event) bool { ... })
package progkit
