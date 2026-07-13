// pkg/wasmhv/browseui/walletconfig.go — the ONE wallet config panel.
//
// The wallet's backend config (storage mode, coin nodes, BTC electrum server,
// skysocks exit) is a set of localStorage keys read by the /wallet/ fetch shim
// (native: hypervisor_handlers_wallet.go; wasm: serve.go) and the in-tab BTC
// gateway. It used to be reimplemented twice — once in browse.js (the ☰ wallet
// window) and once in the Angular wallet tab. This is the single implementation:
// a self-contained page served at /wallet/config that BOTH surfaces embed in an
// <iframe>. Same origin as /wallet/, so it reads+writes the same localStorage,
// and on Apply it posts {type:"skywire-wallet-config"} to its parent so the
// embedder can reload the wallet view.
package browseui

// WalletConfigHTML is the standalone wallet-config page (served at /wallet/config
// by both the native HV and the wasm `hv serve`). Keys, all read by the shims +
// the BTC gateway:
//
//	skywire-wallet-mode     browser | service
//	skywire-coin-nodes      JSON array of coin-node addresses (first = default)
//	skywire-coin-node       the effective node the shim reads (mirrors nodes[0]
//	                        or the service address)
//	skywire-wallet-service  remote skycoin-web server (service mode)
//	skywire-btc-backend     ssl:// electrum server
//	skywire-btc-proxy       skysocks exit PK for the BTC electrum egress
//	                        (required on wasm; optional on native = self-egress)
const WalletConfigHTML = `<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
:root{color-scheme:dark}
body{margin:0;background:#15131c;color:#e8e8f0;font:12px/1.45 -apple-system,system-ui,monospace}
.wrap{padding:.7em .8em;display:flex;flex-direction:column;gap:.6em}
h1{font-size:12px;font-weight:600;margin:0;opacity:.75;letter-spacing:.3px;text-transform:uppercase}
.modes{display:flex;gap:.4em}
.modes button{flex:1;background:#0d0b13;color:#e8e8f0;border:1px solid #3a3352;border-radius:4px;padding:.5em;cursor:pointer;font:inherit}
.modes button.on{border-color:#6f4bd8;color:#cbb8ff;background:rgba(111,75,216,.18)}
.row{display:flex;gap:.4em;align-items:center}
.row label{opacity:.72;min-width:4.5em;text-align:right;white-space:nowrap}
.row input{flex:1;min-width:0;background:#0d0b13;color:#e8e8f0;border:1px solid #3a3352;border-radius:3px;padding:.4em .5em;font:inherit}
.row input:focus{outline:none;border-color:#6f4bd8}
.hint{opacity:.72;line-height:1.4}.hint b{color:#e8e8f0}.mono{font-family:monospace;opacity:.85}
button.add{align-self:flex-start;background:#2a2342;color:#e8e8f0;border:1px solid #3a3352;border-radius:3px;cursor:pointer;font:inherit;padding:.35em .6em}
button.rm{background:transparent;color:#9aa0a6;border:0;cursor:pointer;font:inherit;padding:.2em .5em}button.rm:hover{color:#f7768e}
.apply{align-self:flex-end;background:#6f4bd8;color:#fff;border:0;border-radius:4px;padding:.5em 1.2em;cursor:pointer;font:inherit}
.err{color:#f7768e;display:none}
.sec{border-top:1px solid #2a2342;padding-top:.6em;display:flex;flex-direction:column;gap:.5em}
</style></head><body><div class="wrap">
<div class="modes">
<button id="mb" title="Wallets stored in this browser; pick the coin node it queries.">Browser wallets</button>
<button id="ms" title="Point at a remote skycoin-web server over dmsg.">Remote wallet service</button>
</div>
<div id="browser">
<div class="hint">Wallets are created + stored in <b>this browser</b>. Configure the coin node(s) it queries over dmsg — the first is the default; add one per fibercoin. A <span class="mono">&lt;pk&gt;:&lt;port&gt;</span> dmsg node is dialed directly; an <span class="mono">http(s)://</span>/<span class="mono">.dmsg</span> URL goes via the resolving proxy.</div>
<div id="nodes"></div>
<button class="add" id="addnode">+ Add coin node</button>
<div class="sec">
<div class="hint"><b>Bitcoin</b> (optional): an <span class="mono">ssl://host:port</span> electrum server, reached over the mesh by the visor's BTC gateway. Keys + signing stay in this browser; only chain queries cross.</div>
<div class="row"><label>btc</label><input id="btc" list="btclist" spellcheck="false" placeholder="pick or type an ssl:// electrum server"></div>
<datalist id="btclist">
<option value="ssl://electrum.blockstream.info:50002"></option>
<option value="ssl://fortress.qtornado.com:50002"></option>
<option value="ssl://electrum.emzy.de:50002"></option>
<option value="ssl://electrum.bitaroo.net:50002"></option>
<option value="ssl://bitcoin.lu.ke:50002"></option>
<option value="ssl://electrum.jochen-hoenicke.de:50006"></option>
</datalist>
<div class="hint"><b>Skysocks exit</b> <span id="proxyreq"></span>: the visor PK whose skysocks-server relays the BTC electrum connection to the clearnet. <span id="proxynote"></span></div>
<div class="row"><label>proxy</label><input id="proxy" spellcheck="false" placeholder="skysocks exit PK (66 hex)"></div>
</div>
</div>
<div id="service" style="display:none">
<div class="hint">Point at a remote <b>skycoin-web server</b> over dmsg. Wallets live on that server; all wallet + node API route to it.</div>
<div class="row"><label>service</label><input id="svc" spellcheck="false" placeholder="03d1d78e…:8002"></div>
</div>
<div id="err" class="err"></div>
<button class="apply" id="apply">Apply</button>
</div>
<script>(function(){
var K={mode:"skywire-wallet-mode",nodes:"skywire-coin-nodes",prim:"skywire-coin-node",svc:"skywire-wallet-service",btc:"skywire-btc-backend",proxy:"skywire-btc-proxy"};
function ls(k,d){try{return localStorage.getItem(k)||d;}catch(e){return d;}}
function set(k,v){try{localStorage.setItem(k,v);}catch(e){}}
function $(id){return document.getElementById(id);}
// isWasm is passed by the embedder via ?wasm=1 (BTC egress needs an exit there).
var isWasm=/[?&]wasm=1/.test(location.search);
$("proxyreq").textContent=isWasm?"(required)":"(optional)";
$("proxynote").textContent=isWasm?"The browser can't reach the clearnet itself, so this is required for BTC.":"Empty = this visor does the egress itself (self). Set one to route BTC through another visor for IP privacy.";
var mode=ls(K.mode,"browser");
var nodes;try{nodes=JSON.parse(ls(K.nodes,"")||"null");}catch(e){nodes=null;}
if(!nodes||!nodes.length){var p=ls(K.prim,"");nodes=p?[p]:[""];}
function renderNodes(){var box=$("nodes");box.innerHTML="";nodes.forEach(function(n,i){
var r=document.createElement("div");r.className="row";
var l=document.createElement("label");l.textContent="coin "+i;
var inp=document.createElement("input");inp.spellcheck=false;inp.value=n||"";
inp.placeholder=(i===0?"039a6d1e…:6420 or node.skycoin.com.<pk>.dmsg:6420  (blank = node.skycoin.com)":"039a6d1e…:6420");
inp.oninput=function(){nodes[i]=inp.value;};
r.appendChild(l);r.appendChild(inp);
if(nodes.length>1){var rm=document.createElement("button");rm.className="rm";rm.textContent="✕";rm.title="remove";rm.onclick=function(){nodes.splice(i,1);renderNodes();};r.appendChild(rm);}
box.appendChild(r);});}
function setMode(m){mode=m;$("mb").classList.toggle("on",m==="browser");$("ms").classList.toggle("on",m==="service");
$("browser").style.display=m==="browser"?"":"none";$("service").style.display=m==="service"?"":"none";}
$("btc").value=ls(K.btc,"");$("proxy").value=ls(K.proxy,"");$("svc").value=ls(K.svc,"");
renderNodes();setMode(mode);
$("mb").onclick=function(){setMode("browser");};$("ms").onclick=function(){setMode("service");};
$("addnode").onclick=function(){nodes.push("");renderNodes();};
$("apply").onclick=function(){var e=$("err");e.style.display="none";e.textContent="";
if(mode==="service"){var s=($("svc").value||"").trim();if(!s){e.textContent="Enter the wallet service address.";e.style.display="";return;}
set(K.mode,"service");set(K.svc,s);set(K.prim,s);}
else{var clean=nodes.map(function(n){return(n||"").trim();}).filter(function(n,i){return n||i===0;});if(!clean.length)clean=[""];nodes=clean;
set(K.mode,"browser");set(K.nodes,JSON.stringify(clean));set(K.prim,clean[0]||"");
set(K.btc,($("btc").value||"").trim());set(K.proxy,($("proxy").value||"").trim());}
try{parent.postMessage({type:"skywire-wallet-config"},"*");}catch(_){}
};
})();</script></body></html>`
