//go:build js && wasm

package ui

import (
	"syscall/js"
)

// Canvas wraps an HTML5 Canvas element and its 2D rendering context
type Canvas struct {
	element js.Value
	ctx     js.Value
	width   float64
	height  float64
}

// NewCanvas creates a new Canvas wrapper for the given canvas element ID
func NewCanvas(elementID string) *Canvas {
	doc := js.Global().Get("document")
	element := doc.Call("getElementById", elementID)
	if element.IsNull() || element.IsUndefined() {
		return nil
	}

	ctx := element.Call("getContext", "2d")

	c := &Canvas{
		element: element,
		ctx:     ctx,
	}
	c.UpdateSize()
	return c
}

// UpdateSize updates the internal width/height from the canvas element
func (c *Canvas) UpdateSize() {
	c.width = c.element.Get("width").Float()
	c.height = c.element.Get("height").Float()
}

// Width returns the canvas width
func (c *Canvas) Width() float64 {
	return c.width
}

// Height returns the canvas height
func (c *Canvas) Height() float64 {
	return c.height
}

// Resize sets the canvas size
func (c *Canvas) Resize(width, height int) {
	c.element.Set("width", width)
	c.element.Set("height", height)
	c.width = float64(width)
	c.height = float64(height)
}

// Clear clears the entire canvas with the given color
func (c *Canvas) Clear(color string) {
	c.ctx.Set("fillStyle", color)
	c.ctx.Call("fillRect", 0, 0, c.width, c.height)
}

// FillCircle draws a filled circle
func (c *Canvas) FillCircle(x, y, radius float64, color string) {
	c.ctx.Call("beginPath")
	c.ctx.Call("arc", x, y, radius, 0, 6.283185307179586) // 2*PI
	c.ctx.Set("fillStyle", color)
	c.ctx.Call("fill")
}

// StrokeCircle draws a circle outline
func (c *Canvas) StrokeCircle(x, y, radius, lineWidth float64, color string) {
	c.ctx.Call("beginPath")
	c.ctx.Call("arc", x, y, radius, 0, 6.283185307179586)
	c.ctx.Set("strokeStyle", color)
	c.ctx.Set("lineWidth", lineWidth)
	c.ctx.Call("stroke")
}

// Line draws a line between two points
func (c *Canvas) Line(x1, y1, x2, y2, lineWidth float64, color string) {
	c.ctx.Call("beginPath")
	c.ctx.Call("moveTo", x1, y1)
	c.ctx.Call("lineTo", x2, y2)
	c.ctx.Set("strokeStyle", color)
	c.ctx.Set("lineWidth", lineWidth)
	c.ctx.Call("stroke")
}

// Text draws text at the given position
func (c *Canvas) Text(text string, x, y float64, color string, font string) {
	if font != "" {
		c.ctx.Set("font", font)
	}
	c.ctx.Set("fillStyle", color)
	c.ctx.Call("fillText", text, x, y)
}

// FillRect draws a filled rectangle
func (c *Canvas) FillRect(x, y, width, height float64, color string) {
	c.ctx.Set("fillStyle", color)
	c.ctx.Call("fillRect", x, y, width, height)
}

// SetGlobalAlpha sets the global alpha (transparency)
func (c *Canvas) SetGlobalAlpha(alpha float64) {
	c.ctx.Set("globalAlpha", alpha)
}

// ResetGlobalAlpha resets alpha to 1.0
func (c *Canvas) ResetGlobalAlpha() {
	c.ctx.Set("globalAlpha", 1.0)
}
