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
	c.ctx.Set("lineCap", "round")
	c.ctx.Set("lineJoin", "round")
	c.ctx.Call("beginPath")
	c.ctx.Call("moveTo", x1, y1)
	c.ctx.Call("lineTo", x2, y2)
	c.ctx.Set("strokeStyle", color)
	c.ctx.Set("lineWidth", lineWidth)
	c.ctx.Call("stroke")
}

// QuadraticCurve draws a curved line between two points (matching vis-network 'continuous' smooth)
// Uses a control point perpendicular to the line midpoint with roundness factor
func (c *Canvas) QuadraticCurve(x1, y1, x2, y2, lineWidth float64, color string) {
	// Calculate edge length
	dx := x2 - x1
	dy := y2 - y1
	dist := dx*dx + dy*dy

	// For very short edges, just draw a line
	if dist < 100 {
		c.Line(x1, y1, x2, y2, lineWidth, color)
		return
	}

	// Calculate midpoint
	midX := (x1 + x2) / 2
	midY := (y1 + y2) / 2

	// vis-network 'continuous' smooth with roundness: 0.5
	// This creates a gentle curve perpendicular to the line
	// The actual offset is: perpendicular * length * roundness * 0.2 (internal scaling)
	length := sqrt(dist)
	// Match TypeScript vis-network: roundness 0.5 * internal factor ~0.2 = 0.1 effective
	scaledRoundness := 0.1

	// Normalize the perpendicular direction and scale by edge length and roundness
	perpX := (-dy / length) * length * scaledRoundness
	perpY := (dx / length) * length * scaledRoundness

	// Control point
	ctrlX := midX + perpX
	ctrlY := midY + perpY

	// Set line rendering properties for smoother appearance
	c.ctx.Set("lineCap", "round")
	c.ctx.Set("lineJoin", "round")

	c.ctx.Call("beginPath")
	c.ctx.Call("moveTo", x1, y1)
	c.ctx.Call("quadraticCurveTo", ctrlX, ctrlY, x2, y2)
	c.ctx.Set("strokeStyle", color)
	c.ctx.Set("lineWidth", lineWidth)
	c.ctx.Call("stroke")
}

// sqrt returns the square root (avoiding math import in hot path)
func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	// Newton-Raphson iteration
	z := x / 2
	for i := 0; i < 10; i++ {
		z = (z + x/z) / 2
	}
	return z
}

// DashedQuadraticCurve draws a dashed curved line between two points
func (c *Canvas) DashedQuadraticCurve(x1, y1, x2, y2, lineWidth float64, color string, dashLength, gapLength float64) {
	// Calculate edge length
	dx := x2 - x1
	dy := y2 - y1
	dist := dx*dx + dy*dy

	if dist < 100 {
		c.DashedLine(x1, y1, x2, y2, lineWidth, color, dashLength, gapLength)
		return
	}

	// Calculate midpoint
	midX := (x1 + x2) / 2
	midY := (y1 + y2) / 2

	// Scaled roundness matching solid curves (vis-network roundness: 0.5)
	length := sqrt(dist)
	scaledRoundness := 0.1

	// Normalize and scale perpendicular direction
	perpX := (-dy / length) * length * scaledRoundness
	perpY := (dx / length) * length * scaledRoundness

	// Control point
	ctrlX := midX + perpX
	ctrlY := midY + perpY

	// Set line rendering properties
	c.ctx.Set("lineCap", "round")
	c.ctx.Set("lineJoin", "round")

	c.ctx.Call("beginPath")
	c.ctx.Call("moveTo", x1, y1)
	c.ctx.Call("quadraticCurveTo", ctrlX, ctrlY, x2, y2)
	c.ctx.Set("strokeStyle", color)
	c.ctx.Set("lineWidth", lineWidth)
	c.ctx.Call("setLineDash", js.ValueOf([]interface{}{dashLength, gapLength}))
	c.ctx.Call("stroke")
	c.ctx.Call("setLineDash", js.ValueOf([]interface{}{})) // Reset to solid
}

// DashedLine draws a dashed line between two points
func (c *Canvas) DashedLine(x1, y1, x2, y2, lineWidth float64, color string, dashLength, gapLength float64) {
	c.ctx.Set("lineCap", "round")
	c.ctx.Set("lineJoin", "round")
	c.ctx.Call("beginPath")
	c.ctx.Call("moveTo", x1, y1)
	c.ctx.Call("lineTo", x2, y2)
	c.ctx.Set("strokeStyle", color)
	c.ctx.Set("lineWidth", lineWidth)
	c.ctx.Call("setLineDash", js.ValueOf([]interface{}{dashLength, gapLength}))
	c.ctx.Call("stroke")
	c.ctx.Call("setLineDash", js.ValueOf([]interface{}{})) // Reset to solid
}

// Text draws text at the given position
func (c *Canvas) Text(text string, x, y float64, color string, font string) {
	if font != "" {
		c.ctx.Set("font", font)
	}
	c.ctx.Set("fillStyle", color)
	c.ctx.Call("fillText", text, x, y)
}

// StrokeText draws text outline at the given position (for readability against backgrounds)
func (c *Canvas) StrokeText(text string, x, y float64, color string, lineWidth float64, font string) {
	if font != "" {
		c.ctx.Set("font", font)
	}
	c.ctx.Set("strokeStyle", color)
	c.ctx.Set("lineWidth", lineWidth)
	c.ctx.Call("strokeText", text, x, y)
}

// FillRect draws a filled rectangle
func (c *Canvas) FillRect(x, y, width, height float64, color string) {
	c.ctx.Set("fillStyle", color)
	c.ctx.Call("fillRect", x, y, width, height)
}

// RoundedRect draws a filled rounded rectangle
func (c *Canvas) RoundedRect(x, y, width, height, radius float64, fillColor string) {
	c.ctx.Call("beginPath")
	c.ctx.Call("moveTo", x+radius, y)
	c.ctx.Call("lineTo", x+width-radius, y)
	c.ctx.Call("arcTo", x+width, y, x+width, y+radius, radius)
	c.ctx.Call("lineTo", x+width, y+height-radius)
	c.ctx.Call("arcTo", x+width, y+height, x+width-radius, y+height, radius)
	c.ctx.Call("lineTo", x+radius, y+height)
	c.ctx.Call("arcTo", x, y+height, x, y+height-radius, radius)
	c.ctx.Call("lineTo", x, y+radius)
	c.ctx.Call("arcTo", x, y, x+radius, y, radius)
	c.ctx.Call("closePath")
	c.ctx.Set("fillStyle", fillColor)
	c.ctx.Call("fill")
}

// StrokeRoundedRect draws a rounded rectangle outline
func (c *Canvas) StrokeRoundedRect(x, y, width, height, radius, lineWidth float64, strokeColor string) {
	c.ctx.Call("beginPath")
	c.ctx.Call("moveTo", x+radius, y)
	c.ctx.Call("lineTo", x+width-radius, y)
	c.ctx.Call("arcTo", x+width, y, x+width, y+radius, radius)
	c.ctx.Call("lineTo", x+width, y+height-radius)
	c.ctx.Call("arcTo", x+width, y+height, x+width-radius, y+height, radius)
	c.ctx.Call("lineTo", x+radius, y+height)
	c.ctx.Call("arcTo", x, y+height, x, y+height-radius, radius)
	c.ctx.Call("lineTo", x, y+radius)
	c.ctx.Call("arcTo", x, y, x+radius, y, radius)
	c.ctx.Call("closePath")
	c.ctx.Set("strokeStyle", strokeColor)
	c.ctx.Set("lineWidth", lineWidth)
	c.ctx.Call("stroke")
}

// MeasureText returns the width of a text string in pixels
func (c *Canvas) MeasureText(text string, font string) float64 {
	if font != "" {
		c.ctx.Set("font", font)
	}
	metrics := c.ctx.Call("measureText", text)
	return metrics.Get("width").Float()
}

// Save saves the current canvas state (transform, styles, clipping)
func (c *Canvas) Save() {
	c.ctx.Call("save")
}

// Restore restores the most recently saved canvas state
func (c *Canvas) Restore() {
	c.ctx.Call("restore")
}

// Translate applies a translation to the canvas transform
func (c *Canvas) Translate(x, y float64) {
	c.ctx.Call("translate", x, y)
}

// SetScale applies scaling to the canvas transform
func (c *Canvas) SetScale(sx, sy float64) {
	c.ctx.Call("scale", sx, sy)
}

// SetGlobalAlpha sets the global alpha (transparency)
func (c *Canvas) SetGlobalAlpha(alpha float64) {
	c.ctx.Set("globalAlpha", alpha)
}

// ResetGlobalAlpha resets alpha to 1.0
func (c *Canvas) ResetGlobalAlpha() {
	c.ctx.Set("globalAlpha", 1.0)
}

// SetShadow sets the shadow properties for subsequent drawing operations
func (c *Canvas) SetShadow(color string, blur, offsetX, offsetY float64) {
	c.ctx.Set("shadowColor", color)
	c.ctx.Set("shadowBlur", blur)
	c.ctx.Set("shadowOffsetX", offsetX)
	c.ctx.Set("shadowOffsetY", offsetY)
}

// ClearShadow resets shadow properties
func (c *Canvas) ClearShadow() {
	c.ctx.Set("shadowColor", "transparent")
	c.ctx.Set("shadowBlur", 0)
	c.ctx.Set("shadowOffsetX", 0)
	c.ctx.Set("shadowOffsetY", 0)
}

// SetLineCap sets the line cap style (butt, round, square)
func (c *Canvas) SetLineCap(cap string) {
	c.ctx.Set("lineCap", cap)
}

// SetLineJoin sets the line join style (round, bevel, miter)
func (c *Canvas) SetLineJoin(join string) {
	c.ctx.Set("lineJoin", join)
}

// FillCircleWithShadow draws a filled circle with shadow/glow effect
func (c *Canvas) FillCircleWithShadow(x, y, radius float64, fillColor, shadowColor string, shadowBlur float64) {
	c.SetShadow(shadowColor, shadowBlur, 0, 0)
	c.FillCircle(x, y, radius, fillColor)
	c.ClearShadow()
}

// BezierCurve draws a cubic bezier curve (smoother than quadratic)
func (c *Canvas) BezierCurve(x1, y1, x2, y2, lineWidth float64, color string) {
	dx := x2 - x1
	dy := y2 - y1
	dist := dx*dx + dy*dy
	if dist < 100 {
		c.Line(x1, y1, x2, y2, lineWidth, color)
		return
	}

	// Perpendicular offset for smooth curve
	roundness := 0.3
	perpX := -dy * roundness
	perpY := dx * roundness

	// Two control points for cubic bezier (S-curve shape)
	ctrl1X := x1 + dx*0.25 + perpX*0.5
	ctrl1Y := y1 + dy*0.25 + perpY*0.5
	ctrl2X := x1 + dx*0.75 + perpX*0.5
	ctrl2Y := y1 + dy*0.75 + perpY*0.5

	c.ctx.Call("beginPath")
	c.ctx.Call("moveTo", x1, y1)
	c.ctx.Call("bezierCurveTo", ctrl1X, ctrl1Y, ctrl2X, ctrl2Y, x2, y2)
	c.ctx.Set("strokeStyle", color)
	c.ctx.Set("lineWidth", lineWidth)
	c.SetLineCap("round")
	c.ctx.Call("stroke")
}

// FillPolygon draws a filled polygon from a slice of points
func (c *Canvas) FillPolygon(points []Point, color string) {
	if len(points) < 3 {
		return
	}
	c.ctx.Call("beginPath")
	c.ctx.Call("moveTo", points[0].X, points[0].Y)
	for i := 1; i < len(points); i++ {
		c.ctx.Call("lineTo", points[i].X, points[i].Y)
	}
	c.ctx.Call("closePath")
	c.ctx.Set("fillStyle", color)
	c.ctx.Call("fill")
}

// StrokePolygon draws a polygon outline from a slice of points
func (c *Canvas) StrokePolygon(points []Point, lineWidth float64, color string) {
	if len(points) < 3 {
		return
	}
	c.ctx.Call("beginPath")
	c.ctx.Call("moveTo", points[0].X, points[0].Y)
	for i := 1; i < len(points); i++ {
		c.ctx.Call("lineTo", points[i].X, points[i].Y)
	}
	c.ctx.Call("closePath")
	c.ctx.Set("strokeStyle", color)
	c.ctx.Set("lineWidth", lineWidth)
	c.ctx.Call("stroke")
}
