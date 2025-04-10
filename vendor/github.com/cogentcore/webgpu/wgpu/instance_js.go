//go:build js

package wgpu

import (
	"fmt"
	"log"
	"syscall/js"
)

// Instance as described:
// https://gpuweb.github.io/gpuweb/#gpu-interface
// (Instance is called GPU in js)
type Instance struct {
	jsValue js.Value
}

func CreateInstance(descriptor *InstanceDescriptor) *Instance {
	gpu := js.Global().Get("navigator").Get("gpu")
	if !gpu.Truthy() {
		log.Println("WebGPU not supported")
		return nil
	}
	return &Instance{jsValue: gpu}
}

func (g Instance) RequestAdapter(options *RequestAdapterOptions) (*Adapter, error) {
	adapter := await(g.jsValue.Call("requestAdapter", pointerToJS(options)))
	if !adapter.Truthy() {
		return nil, fmt.Errorf("no WebGPU adapter avaliable")
	}
	return &Adapter{jsValue: adapter}, nil
}

func (g Instance) EnumerateAdapters(options *InstanceEnumerateAdapterOptons) []*Adapter {
	a, err := g.RequestAdapter(&RequestAdapterOptions{})
	if err != nil {
		log.Println(err)
		return nil
	}
	return []*Adapter{a}
}

type SurfaceDescriptor struct {
	Label string
}

func (g Instance) CreateSurface(descriptor *SurfaceDescriptor) *Surface {
	jsContext := js.Global().Get("document").Call("querySelector", "canvas").Call("getContext", "webgpu")
	return &Surface{jsContext}
}

func (g Instance) GenerateReport() any { return nil } // no-op

func (g Instance) Release() {} // no-op
