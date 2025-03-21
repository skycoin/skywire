//go:build js

package wgpu

import (
	"syscall/js"
)

// Queue as described:
// https://gpuweb.github.io/gpuweb/#gpuqueue
type Queue struct {
	jsValue js.Value
}

func (g Queue) toJS() any {
	return g.jsValue
}

// Submit as described:
// https://gpuweb.github.io/gpuweb/#dom-gpuqueue-submit
func (g Queue) Submit(commandBuffers ...*CommandBuffer) {
	jsSequence := mapSlice(commandBuffers, func(buffer *CommandBuffer) any {
		return pointerToJS(buffer)
	})
	g.jsValue.Call("submit", jsSequence)
}

// WriteBuffer as described:
// https://gpuweb.github.io/gpuweb/#dom-gpuqueue-writebuffer
func (g Queue) WriteBuffer(buffer *Buffer, offset uint64, data []byte) (err error) {
	g.jsValue.Call("writeBuffer", pointerToJS(buffer), offset, BytesToJS(data), uint64(0), len(data))
	return
}

// WriteTexture as described:
// https://gpuweb.github.io/gpuweb/#dom-gpuqueue-writetexture
func (g Queue) WriteTexture(destination *ImageCopyTexture, data []byte, dataLayout *TextureDataLayout, writeSize *Extent3D) (err error) {
	g.jsValue.Call("writeTexture", pointerToJS(destination), BytesToJS(data), pointerToJS(dataLayout), pointerToJS(writeSize))
	return
}

// OnSubmittedWorkDone as described:
// https://gpuweb.github.io/gpuweb/#dom-gpuqueue-onsubmittedworkdone
func (g Queue) OnSubmittedWorkDone(callback QueueWorkDoneCallback) {
	await(g.jsValue.Call("onSubmittedWorkDone")) // TODO(kai): is this correct?
	callback(QueueWorkDoneStatusSuccess)
}

func (g Queue) Release() {} // no-op
