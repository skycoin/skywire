//go:build js && wasm

package winbox

import "syscall/js"

const templateHTML = `<div class=wb-header>` +
	`<div class=wb-control>` +
	`<span class=wb-min></span>` +
	`<span class=wb-max></span>` +
	`<span class=wb-full></span>` +
	`<span class=wb-close></span>` +
	`</div>` +
	`<div class=wb-drag>` +
	`<div class=wb-icon></div>` +
	`<div class=wb-title></div>` +
	`</div>` +
	`</div>` +
	`<div class=wb-body></div>` +
	`<div class=wb-n></div>` +
	`<div class=wb-s></div>` +
	`<div class=wb-w></div>` +
	`<div class=wb-e></div>` +
	`<div class=wb-nw></div>` +
	`<div class=wb-ne></div>` +
	`<div class=wb-se></div>` +
	`<div class=wb-sw></div>`

var templateNode js.Value

// template returns a fresh clone of the window scaffold. A custom template
// element can be passed via Options.Template.
func template(tpl js.Value) js.Value {
	if tpl.Truthy() {
		return tpl.Call("cloneNode", true)
	}
	if !templateNode.Truthy() {
		templateNode = document.Call("createElement", "div")
		templateNode.Set("innerHTML", templateHTML)
	}
	return templateNode.Call("cloneNode", true)
}
