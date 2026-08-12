//go:build js && wasm

package cosmos

import (
	"math"
	"syscall/js"
)

// zoomTransform mirrors the d3-zoom ZoomTransform {k, x, y}.
type zoomTransform struct {
	k, x, y float64
}

var zoomIdentity = zoomTransform{k: 1}

func (t zoomTransform) applyX(x float64) float64 { return x*t.k + t.x }
func (t zoomTransform) applyY(y float64) float64 { return y*t.k + t.y }
func (t zoomTransform) invert(p [2]float64) [2]float64 {
	return [2]float64{(p[0] - t.x) / t.k, (p[1] - t.y) / t.k}
}

const (
	scaleExtentMin = 0.001
	wheelIdleDelay = 150.0 // ms, d3-zoom's wheel gesture idle timeout
)

// zoomState is the port of the Zoom module plus the subset of d3-zoom and
// d3-drag behavior cosmos relies on: wheel zoom, drag pan, double-click
// zoom, touch pan/pinch, point dragging, and programmatic transforms with
// smooth transitions (van Wijk & Nuij interpolation, as d3.interpolateZoom).
type zoomState struct {
	st  *store
	cfg *Config

	canvas         js.Value
	eventTransform zoomTransform
	isRunning      bool
	wheelEnabled   bool

	// hooks wired by the Graph (detect handlers + user events)
	onStart func(sourceEvent js.Value)
	onZoom  func(sourceEvent js.Value)
	onEnd   func(sourceEvent js.Value)

	// point dragging (the d3-drag behavior of the original)
	dragActive  bool
	dragSubject func() bool // whether a drag should start instead of a pan
	onDragStart func(event js.Value)
	onDrag      func(event js.Value)
	onDragEnd   func(event js.Value)

	// mouse pan gesture
	mousePanning bool
	panAnchor    [2]float64 // space point under the cursor at gesture start

	// wheel gesture
	wheelGestureOn bool
	wheelTimerID   js.Value

	// touch gesture
	touchIDs    [2]int
	touchAnchor [2][2]float64 // space anchors
	touchCount  int

	// active transition (nil when idle)
	trans *zoomTrans

	funcs []js.Func
}

type zoomTrans struct {
	startTime float64 // -1 until first tick
	duration  float64
	ease      func(float64) float64
	from, to  zoomTransform
	next      *zoomTrans
}

func newZoomState(st *store, cfg *Config) *zoomState {
	return &zoomState{st: st, cfg: cfg, eventTransform: zoomIdentity, wheelEnabled: true}
}

// setTransformNow applies a transform, updating the store matrix and firing
// the zoom hook.
func (z *zoomState) setTransformNow(t zoomTransform, sourceEvent js.Value) {
	z.eventTransform = t
	w := z.st.screenSize[0]
	h := z.st.screenSize[1]
	if w == 0 || h == 0 {
		return
	}
	m := &z.st.transform
	m.projection(w, h)
	m.translate(t.x, t.y)
	m.scale(t.k, t.k)
	m.translate(w/2, h/2)
	m.scale(w/2, h/2)
	m.scale(1, -1)
	if z.onZoom != nil {
		z.onZoom(sourceEvent)
	}
}

func (z *zoomState) startGesture(sourceEvent js.Value) {
	if !z.isRunning {
		z.isRunning = true
		if z.onStart != nil {
			z.onStart(sourceEvent)
		}
	}
}

func (z *zoomState) endGesture(sourceEvent js.Value) {
	if z.isRunning {
		z.isRunning = false
		if z.onEnd != nil {
			z.onEnd(sourceEvent)
		}
	}
}

func (z *zoomState) interruptTransition() {
	z.trans = nil
}

// transformTo applies the target transform, animated over duration
// milliseconds (0 = instant).
func (z *zoomState) transformTo(target zoomTransform, duration float64, ease func(float64) float64) {
	z.transformChain([]zoomTransform{target}, []float64{duration}, []func(float64) float64{ease})
}

func (z *zoomState) transformChain(targets []zoomTransform, durations []float64, eases []func(float64) float64) {
	z.interruptTransition()
	if len(targets) == 0 {
		return
	}
	if durations[0] <= 0 {
		// synchronous, like a non-transition zoom.transform call
		z.startGesture(js.Undefined())
		z.setTransformNow(targets[0], js.Undefined())
		z.endGesture(js.Undefined())
		return
	}
	var head, tail *zoomTrans
	for i, target := range targets {
		e := eases[i]
		if e == nil {
			e = easeQuadInOut
		}
		t := &zoomTrans{startTime: -1, duration: durations[i], ease: e, to: target}
		if head == nil {
			head = t
		} else {
			tail.next = t
		}
		tail = t
	}
	head.from = z.eventTransform
	z.trans = head
	z.startGesture(js.Undefined())
}

// tick advances the active transition; called from the frame loop.
func (z *zoomState) tick(now float64) {
	tr := z.trans
	if tr == nil {
		return
	}
	if tr.startTime < 0 {
		tr.startTime = now
		tr.from = z.eventTransform
	}
	el := (now - tr.startTime) / tr.duration
	if el >= 1 {
		z.setTransformNow(tr.to, js.Undefined())
		if tr.next != nil {
			z.trans = tr.next
		} else {
			z.trans = nil
			z.endGesture(js.Undefined())
		}
		return
	}
	z.setTransformNow(z.interpolateTransform(tr.from, tr.to, tr.ease(el)), js.Undefined())
}

// interpolateTransform interpolates between two transforms the way d3-zoom
// does for transitions: via interpolateZoom on the views around the canvas
// center.
func (z *zoomState) interpolateTransform(a, b zoomTransform, t float64) zoomTransform {
	if t >= 1 {
		return b
	}
	w := z.st.screenSize[0]
	h := z.st.screenSize[1]
	p := [2]float64{w / 2, h / 2}
	size := math.Max(w, h)
	ia := a.invert(p)
	ib := b.invert(p)
	l := interpolateZoomViews(
		[3]float64{ia[0], ia[1], size / a.k},
		[3]float64{ib[0], ib[1], size / b.k},
		t,
	)
	k := size / l[2]
	return zoomTransform{k: k, x: p[0] - l[0]*k, y: p[1] - l[1]*k}
}

// interpolateZoomViews is d3.interpolateZoom (van Wijk & Nuij smooth
// zooming) evaluated at time t for views [cx, cy, width].
func interpolateZoomViews(p0, p1 [3]float64, t float64) [3]float64 {
	const rho = math.Sqrt2
	const rho2 = 2.0
	const rho4 = 4.0
	const epsilon2 = 1e-12

	ux0, uy0, w0 := p0[0], p0[1], p0[2]
	ux1, uy1, w1 := p1[0], p1[1], p1[2]
	dx := ux1 - ux0
	dy := uy1 - uy0
	d2 := dx*dx + dy*dy

	if d2 < epsilon2 {
		s := math.Log(w1/w0) / rho
		return [3]float64{
			ux0 + t*dx,
			uy0 + t*dy,
			w0 * math.Exp(rho*t*s),
		}
	}
	d1 := math.Sqrt(d2)
	b0 := (w1*w1 - w0*w0 + rho4*d2) / (2 * w0 * rho2 * d1)
	b1 := (w1*w1 - w0*w0 - rho4*d2) / (2 * w1 * rho2 * d1)
	r0 := math.Log(math.Sqrt(b0*b0+1) - b0)
	r1 := math.Log(math.Sqrt(b1*b1+1) - b1)
	s := (r1 - r0) / rho
	tS := t * s
	coshr0 := math.Cosh(r0)
	u := w0 / (rho2 * d1) * (coshr0*math.Tanh(rho*tS+r0) - math.Sinh(r0))
	return [3]float64{
		ux0 + u*dx,
		uy0 + u*dy,
		w0 * coshr0 / math.Cosh(rho*tS+r0),
	}
}

// scaleTo zooms to level k around the canvas center.
func (z *zoomState) scaleTo(k float64, duration float64) {
	w := z.st.screenSize[0]
	h := z.st.screenSize[1]
	p := [2]float64{w / 2, h / 2}
	t := z.eventTransform
	q := t.invert(p)
	k = math.Max(scaleExtentMin, k)
	target := zoomTransform{k: k, x: p[0] - q[0]*k, y: p[1] - q[1]*k}
	z.transformTo(target, duration, easeQuadInOut)
}

// ------------------------------------------------------------------ Zoom
// module methods (getTransform & friends)

// getTransform returns the zoom transform that fits the given point
// positions (space coordinates) into the viewport.
func (z *zoomState) getTransform(positions [][2]float64, scale float64, hasScale bool, padding float64) zoomTransform {
	if len(positions) == 0 {
		return z.eventTransform
	}
	width := z.st.screenSize[0]
	height := z.st.screenSize[1]
	xs := make([]float64, len(positions))
	ys := make([]float64, len(positions))
	for i, p := range positions {
		xs[i] = p[0]
		ys[i] = p[1]
	}
	xMin, xMax, _ := extent(xs)
	yMin, yMax, _ := extent(ys)
	xMin = z.st.scaleX(xMin)
	xMax = z.st.scaleX(xMax)
	yMin = z.st.scaleY(yMin)
	yMax = z.st.scaleY(yMax)
	// adjust extent with one screen pixel if only one point coordinate is set
	if xMin == xMax {
		xMin -= 0.5
		xMax += 0.5
	}
	if yMin == yMax {
		yMin += 0.5
		yMax -= 0.5
	}

	xScale := (width * (1 - padding*2)) / (xMax - xMin)
	yScale := (height * (1 - padding*2)) / (yMin - yMax)
	k := scale
	if !hasScale {
		k = math.Min(xScale, yScale)
	}
	clampedScale := math.Max(scaleExtentMin, k)
	xCenter := (xMax + xMin) / 2
	yCenter := (yMax + yMin) / 2
	translateX := width/2 - xCenter*clampedScale
	translateY := height/2 - yCenter*clampedScale

	return zoomTransform{k: clampedScale, x: translateX, y: translateY}
}

func (z *zoomState) getDistanceToPoint(position [2]float64) float64 {
	t := z.eventTransform
	point := z.getTransform([][2]float64{position}, t.k, true, 0.1)
	dx := t.x - point.x
	dy := t.y - point.y
	return math.Sqrt(dx*dx + dy*dy)
}

func (z *zoomState) getMiddlePointTransform(position [2]float64) zoomTransform {
	width := z.st.screenSize[0]
	height := z.st.screenSize[1]
	t := z.eventTransform
	currX := (width/2 - t.x) / t.k
	currY := (height/2 - t.y) / t.k
	pointX := z.st.scaleX(position[0])
	pointY := z.st.scaleY(position[1])
	centerX := (currX + pointX) / 2
	centerY := (currY + pointY) / 2

	scale := 1.0
	translateX := width/2 - centerX*scale
	translateY := height/2 - centerY*scale

	return zoomTransform{k: scale, x: translateX, y: translateY}
}

func (z *zoomState) convertScreenToSpacePosition(screenPosition [2]float64) [2]float64 {
	t := z.eventTransform
	w := z.st.screenSize[0]
	h := z.st.screenSize[1]
	invertedX := (screenPosition[0] - t.x) / t.k
	invertedY := (screenPosition[1] - t.y) / t.k
	spacePosition := [2]float64{invertedX, h - invertedY}
	spacePosition[0] -= (w - z.st.adjustedSpaceSize) / 2
	spacePosition[1] -= (h - z.st.adjustedSpaceSize) / 2
	return spacePosition
}

func (z *zoomState) convertSpaceToScreenPosition(spacePosition [2]float64) [2]float64 {
	return [2]float64{
		z.eventTransform.applyX(z.st.scaleX(spacePosition[0])),
		z.eventTransform.applyY(z.st.scaleY(spacePosition[1])),
	}
}

func (z *zoomState) convertSpaceToScreenRadius(spaceRadius float64) float64 {
	size := spaceRadius * 2
	if z.cfg.ScalePointsOnZoom {
		size *= z.eventTransform.k
	} else {
		size *= math.Min(5.0, math.Max(1.0, z.eventTransform.k*0.01))
	}
	return math.Min(size, z.st.maxPointSize) / 2
}

// ------------------------------------------------------------- gestures

func eventOffset(canvas js.Value, event js.Value) [2]float64 {
	// offsetX/Y relative to the canvas; fall back to client - bounding rect
	// (touch events have no offsetX)
	ox := event.Get("offsetX")
	if ox.Truthy() || (!ox.IsUndefined() && !ox.IsNull()) {
		return [2]float64{ox.Float(), event.Get("offsetY").Float()}
	}
	rect := canvas.Call("getBoundingClientRect")
	return [2]float64{
		event.Get("clientX").Float() - rect.Get("left").Float(),
		event.Get("clientY").Float() - rect.Get("top").Float(),
	}
}

// attach wires the DOM listeners (the d3-zoom + d3-drag behaviors).
func (z *zoomState) attach(canvas js.Value) {
	z.canvas = canvas
	win := js.Global()

	listen := func(target js.Value, event string, passive bool, fn func(js.Value)) {
		f := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			fn(args[0])
			return nil
		})
		z.funcs = append(z.funcs, f)
		opts := js.Global().Get("Object").New()
		opts.Set("passive", passive)
		target.Call("addEventListener", event, f, opts)
	}

	// wheel zoom
	listen(canvas, "wheel", false, func(event js.Value) {
		if !z.wheelEnabled {
			return
		}
		event.Call("preventDefault")
		z.interruptTransition()
		p := eventOffset(canvas, event)
		t := z.eventTransform
		q := t.invert(p)
		k := math.Max(scaleExtentMin, t.k*math.Pow(2, wheelDelta(event)))
		target := zoomTransform{k: k, x: p[0] - q[0]*k, y: p[1] - q[1]*k}
		if !z.wheelGestureOn {
			z.wheelGestureOn = true
			z.startGesture(event)
		}
		z.setTransformNow(target, event)
		z.scheduleWheelEnd()
	})

	// drag pan / point drag
	listen(canvas, "mousedown", true, func(event js.Value) {
		if event.Get("button").Int() != 0 || event.Get("ctrlKey").Bool() {
			return
		}
		z.interruptTransition()
		if z.cfg.EnableDrag && z.dragSubject != nil && z.dragSubject() {
			z.dragActive = true
			if z.onDragStart != nil {
				z.onDragStart(event)
			}
			return
		}
		z.mousePanning = true
		p := eventOffset(canvas, event)
		z.panAnchor = z.eventTransform.invert(p)
		z.startGesture(event)
	})
	listen(win, "mousemove", true, func(event js.Value) {
		if z.dragActive {
			if z.onDrag != nil {
				z.onDrag(event)
			}
			return
		}
		if !z.mousePanning {
			return
		}
		p := eventOffset(canvas, event)
		t := z.eventTransform
		z.setTransformNow(zoomTransform{
			k: t.k,
			x: p[0] - z.panAnchor[0]*t.k,
			y: p[1] - z.panAnchor[1]*t.k,
		}, event)
	})
	listen(win, "mouseup", true, func(event js.Value) {
		if z.dragActive {
			z.dragActive = false
			if z.onDragEnd != nil {
				z.onDragEnd(event)
			}
			return
		}
		if z.mousePanning {
			z.mousePanning = false
			z.endGesture(event)
		}
	})

	// double-click zoom (d3 default: scale ×2, shift ÷2, 250 ms transition)
	listen(canvas, "dblclick", false, func(event js.Value) {
		event.Call("preventDefault")
		p := eventOffset(canvas, event)
		t := z.eventTransform
		q := t.invert(p)
		factor := 2.0
		if event.Get("shiftKey").Bool() {
			factor = 0.5
		}
		k := math.Max(scaleExtentMin, t.k*factor)
		target := zoomTransform{k: k, x: p[0] - q[0]*k, y: p[1] - q[1]*k}
		z.transformTo(target, 250, easeQuadInOut)
	})

	// touch pan / pinch
	listen(canvas, "touchstart", false, func(event js.Value) {
		event.Call("preventDefault")
		z.interruptTransition()
		touches := event.Get("touches")
		n := touches.Get("length").Int()
		if n == 0 {
			return
		}
		z.touchCount = n
		if n > 2 {
			z.touchCount = 2
		}
		for i := 0; i < z.touchCount; i++ {
			touch := touches.Index(i)
			z.touchIDs[i] = touch.Get("identifier").Int()
			p := eventOffset(canvas, touch)
			z.touchAnchor[i] = z.eventTransform.invert(p)
		}
		z.startGesture(event)
	})
	listen(canvas, "touchmove", false, func(event js.Value) {
		if z.touchCount == 0 {
			return
		}
		event.Call("preventDefault")
		touches := event.Get("touches")
		n := touches.Get("length").Int()
		find := func(id int) (js.Value, bool) {
			for i := 0; i < n; i++ {
				touch := touches.Index(i)
				if touch.Get("identifier").Int() == id {
					return touch, true
				}
			}
			return js.Value{}, false
		}
		t := z.eventTransform
		if z.touchCount >= 2 {
			t0, ok0 := find(z.touchIDs[0])
			t1, ok1 := find(z.touchIDs[1])
			if ok0 && ok1 {
				p0 := eventOffset(canvas, t0)
				p1 := eventOffset(canvas, t1)
				q0 := z.touchAnchor[0]
				q1 := z.touchAnchor[1]
				dp := math.Hypot(p1[0]-p0[0], p1[1]-p0[1])
				dq := math.Hypot(q1[0]-q0[0], q1[1]-q0[1])
				k := t.k
				if dq > 0 {
					k = math.Max(scaleExtentMin, dp/dq)
				}
				midP := [2]float64{(p0[0] + p1[0]) / 2, (p0[1] + p1[1]) / 2}
				midQ := [2]float64{(q0[0] + q1[0]) / 2, (q0[1] + q1[1]) / 2}
				z.setTransformNow(zoomTransform{k: k, x: midP[0] - midQ[0]*k, y: midP[1] - midQ[1]*k}, event)
				return
			}
		}
		if touch, ok := find(z.touchIDs[0]); ok {
			p := eventOffset(canvas, touch)
			q := z.touchAnchor[0]
			z.setTransformNow(zoomTransform{k: t.k, x: p[0] - q[0]*t.k, y: p[1] - q[1]*t.k}, event)
		}
	})
	listen(canvas, "touchend", true, func(event js.Value) {
		if z.touchCount > 0 && event.Get("touches").Get("length").Int() == 0 {
			z.touchCount = 0
			z.endGesture(event)
		}
	})
}

// wheelDelta is d3-zoom's default wheelDelta.
func wheelDelta(event js.Value) float64 {
	deltaY := event.Get("deltaY").Float()
	deltaMode := event.Get("deltaMode").Int()
	factor := 0.002
	if deltaMode == 1 {
		factor = 0.05
	} else if deltaMode != 0 {
		factor = 1
	}
	if event.Get("ctrlKey").Bool() {
		factor *= 10
	}
	return -deltaY * factor
}

func (z *zoomState) scheduleWheelEnd() {
	global := js.Global()
	if z.wheelTimerID.Truthy() {
		global.Call("clearTimeout", z.wheelTimerID)
	}
	var f js.Func
	f = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		z.wheelGestureOn = false
		z.wheelTimerID = js.Value{}
		z.endGesture(js.Undefined())
		f.Release()
		return nil
	})
	z.wheelTimerID = global.Call("setTimeout", f, wheelIdleDelay)
}
