// Package browseui pkg/wasmhv/browseui/walletconfig.go c3-vis-wasm
// pkg/wasmhv/browseui/walletconfig.go — the ONE wallet config panel.
//
// The wallet's TRANSPORT/backend config (storage mode, BTC electrum server,
// skysocks exit) is a set of localStorage keys read by the /wallet/ fetch shim
// (native: hypervisor_handlers_wallet.go; wasm: serve.go) and the in-tab BTC
// gateway. Per-coin NODE selection now lives in skycoin-web's own Settings →
// Nodes (customNodeUrls); the shim honors it, so this panel no longer duplicates
// it (see the node-config convergence). It used to be reimplemented twice — once
// in browse.js (the ☰ wallet window) and once in the Angular wallet tab. This is
// the single implementation:
// a self-contained page served at /wallet/config that BOTH surfaces embed in an
// <iframe>. Same origin as /wallet/, so it reads+writes the same localStorage,
// and on Apply it posts {type:"skywire-wallet-config"} to its parent so the
// embedder can reload the wallet view.
package browseui

// WalletConfigHTML is the standalone wallet-config page (served at /wallet/config
// by both the native HV and the wasm `hv serve`). Keys, all read by the shims +
// the BTC gateway:
//
//	skywire-wallet-mode     browser | disk | service   (custody: browser | disk | remote)
//	skywire-coin-nodes      JSON array of coin-node addresses (first = default)
//	skywire-coin-node       the effective node the shim reads (mirrors nodes[0]
//	                        or the service address)
//	skywire-wallet-service  remote skycoin-web server (service mode)
//	skywire-wallet-dir      disk-custody seed-wallet store path (disk mode).
//	                        The disk option is only offered when the embedder
//	                        signals capability via ?disk=1 (a native visor with
//	                        a filesystem + skycoin-web backend); the served PWA
//	                        and browser-tab wasm visor never send it. Realizing
//	                        disk custody in the backend is Stage 3 of the design.
//	skywire-btc-proxy       skysocks exit PK for the BTC electrum egress
//	                        (required on wasm; optional on native = self-egress)
//
// The BTC electrum server is NOT set here anymore — it is the Bitcoin coin's
// node URL in skycoin-web's own Settings → Nodes (localStorage["nodeUrls"], coin
// id -2), exactly like every other coin. Both shims read it from there (falling
// back to the legacy skywire-btc-backend key, then a public default), so this
// panel only owns the skysocks exit the visor routes that egress through.
const WalletConfigHTML = `<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
:root{color-scheme:dark}
*{box-sizing:border-box}
body{margin:0;background:#15131c;color:#e8e8f0;font:13.5px/1.55 -apple-system,system-ui,sans-serif}
.wrap{max-width:760px;margin:0 auto;padding:1.3em 1.5em;display:flex;flex-direction:column;gap:1.05em}
h1{font-size:13px;font-weight:600;margin:0;opacity:.75;letter-spacing:.3px;text-transform:uppercase}
.modes{display:flex;gap:.6em}
.modes button{flex:1;background:#0d0b13;color:#e8e8f0;border:1px solid #3a3352;border-radius:6px;padding:.75em;cursor:pointer;font:inherit}
.modes button.on{border-color:#6f4bd8;color:#cbb8ff;background:rgba(111,75,216,.18)}
.row{display:flex;gap:.65em;align-items:center}
.row label{opacity:.72;min-width:5.5em;text-align:right;white-space:nowrap}
.row input{flex:1;min-width:0;background:#0d0b13;color:#e8e8f0;border:1px solid #3a3352;border-radius:5px;padding:.6em .75em;font:inherit}
.row input:focus{outline:none;border-color:#6f4bd8}
.hint{opacity:.72;line-height:1.5}.hint b{color:#e8e8f0}.mono{font-family:monospace;opacity:.85}
button.add{align-self:flex-start;background:#2a2342;color:#e8e8f0;border:1px solid #3a3352;border-radius:5px;cursor:pointer;font:inherit;padding:.55em .9em}
button.rm{background:transparent;color:#9aa0a6;border:0;cursor:pointer;font:inherit;padding:.35em .6em}button.rm:hover{color:#f7768e}
.apply{align-self:flex-end;background:#6f4bd8;color:#fff;border:0;border-radius:6px;padding:.7em 1.6em;cursor:pointer;font:inherit;font-weight:600}
.err{color:#f7768e;display:none}
.sec{border-top:1px solid #2a2342;padding-top:1em;display:flex;flex-direction:column;gap:.7em}
.row select{flex:1;min-width:0;background:#0d0b13;color:#e8e8f0;border:1px solid #3a3352;border-radius:5px;padding:.55em;font:inherit}
.plog{display:none;margin:.2em 0 0;height:130px;overflow:auto;background:#0e0c14;color:#a9b1d6;border:1px solid #2a2342;border-radius:5px;padding:.55em;font:11px/1.5 monospace;white-space:pre-wrap;word-break:break-all}
.plog.on{display:block}
</style></head><body><div class="wrap">
<h1>Wallet storage</h1>
<div class="modes">
<button id="mb" title="Keys + wallet files stay in this browser. The host never sees them.">Browser</button>
<button id="md" style="display:none" title="Wallet files on this host's filesystem, served by the visor's skycoin-web backend. The host holds the keys.">This host (disk)</button>
<button id="ms" title="Point at another visor's skycoin-web server over dmsg.">Remote</button>
</div>
<div id="browser">
<div class="hint">Wallets are created + stored in <b>this browser</b>. Each coin's <b>node</b> — including <b>Bitcoin's</b> <span class="mono">ssl://</span> electrum server — is set in the wallet's own <b>Settings → Nodes</b> (per coin: a mesh node <span class="mono">http://&lt;name&gt;.&lt;base32-pk&gt;.dmsg</span> or a clearnet URL). This panel keeps only the <b>skysocks exit</b> the visor routes that clearnet traffic through.</div>
<div class="sec">
<div class="hint"><b>Skysocks exit</b> <span id="proxyreq"></span>: the visor PK whose skysocks-server relays the clearnet connection — the BTC electrum egress AND the iframe browser share this exit. <span id="proxynote"></span></div>
<div class="row"><label>proxy</label><input id="proxy" spellcheck="false" placeholder="skysocks exit PK (66 hex)"><button class="add" id="psrv" title="list skysocks-server PKs from service discovery">servers ▾</button><button class="add" id="prnd" title="pick a random skysocks server from service discovery">🎲 random</button></div>
<div class="row" id="pselrow" style="display:none"><label></label><select id="psel"></select></div>
<pre id="plog" class="plog" title="live: resolving proxy (dmsg) + skysocks-lite (clearnet)"></pre>
</div>
</div>
<div id="disk" style="display:none">
<div class="hint" style="color:#ffb454"><b>⚠ This flips the trust model.</b> With disk storage the wallet files live on <b>this host's filesystem</b> and the visor's skycoin-web backend holds your keys — unlike Browser, where keys never leave your browser. Only use disk on a machine you control.</div>
<div class="row"><label>dir</label><input id="dir" spellcheck="false" placeholder="~/.skycoin/wallets (seed-wallet store — one dir, many seed files)"></div>
<div class="hint">A <b>seed-wallet store</b>: one directory holding N seed files, <i>not</i> one dir per coin (a bip44 seed already spans several chains). Empty = the backend default.</div>
</div>
<div id="service" style="display:none">
<div class="hint">Point at a remote <b>skycoin-web server</b> over dmsg. Wallets live on that server; all wallet + node API route to it.</div>
<div class="row"><label>service</label><input id="svc" spellcheck="false" placeholder="03d1d78e…:8002"></div>
</div>
<div id="err" class="err"></div>
<button class="apply" id="apply">Apply</button>
</div>
<script>(function(){
var K={mode:"skywire-wallet-mode",nodes:"skywire-coin-nodes",prim:"skywire-coin-node",svc:"skywire-wallet-service",proxy:"skywire-btc-proxy",dir:"skywire-wallet-dir"};
function ls(k,d){try{return localStorage.getItem(k)||d;}catch(e){return d;}}
function set(k,v){try{localStorage.setItem(k,v);}catch(e){}}
function $(id){return document.getElementById(id);}
// isWasm is passed by the embedder via ?wasm=1 (BTC egress needs an exit there).
var isWasm=/[?&]wasm=1/.test(location.search);
// diskCapable: the embedder passes ?disk=1 ONLY when this context can realize
// disk custody — a native visor with a filesystem + the skycoin-web backend.
// A served PWA (hv serve) and the browser-tab wasm visor have no per-visor FS,
// so they never send it and the disk option stays hidden (capability gating —
// see WalletCustodyOptions in visorconfig).
var diskCapable=/[?&]disk=1/.test(location.search);
if(diskCapable)$("md").style.display="";
$("proxyreq").textContent=isWasm?"(required)":"(optional)";
$("proxynote").textContent=isWasm?"The browser can't reach the clearnet itself, so this is required for BTC.":"Empty = this visor does the egress itself (self). Set one to route BTC through another visor for IP privacy.";
var mode=ls(K.mode,"browser");
if(mode==="disk"&&!diskCapable)mode="browser"; // clamp a disk config opened in a non-disk context
function setMode(m){mode=m;
$("mb").classList.toggle("on",m==="browser");$("md").classList.toggle("on",m==="disk");$("ms").classList.toggle("on",m==="service");
$("browser").style.display=m==="browser"?"":"none";$("disk").style.display=m==="disk"?"":"none";$("service").style.display=m==="service"?"":"none";}
$("proxy").value=ls(K.proxy,"")||ls("skywire-upstream-proxy","");$("svc").value=ls(K.svc,"");$("dir").value=ls(K.dir,"");
setMode(mode);
$("mb").onclick=function(){setMode("browser");};$("md").onclick=function(){setMode("disk");};$("ms").onclick=function(){setMode("service");};
// Skysocks exit: SD-populated dropdown + live proxy/resolving-proxy log — the
// same machinery the iframe browser (browse.js) exposes, applied to the wallet.
function visor(){try{return parent&&parent.skywireVisor;}catch(e){return null;}}
var plogBuf=[];
function plog(s){var t="";try{t=new Date().toTimeString().slice(0,8);}catch(e){}plogBuf.push(t+"  "+s);if(plogBuf.length>300)plogBuf.shift();var el=$("plog");if(el){el.classList.add("on");el.textContent=plogBuf.join("\n");el.scrollTop=el.scrollHeight;}}
try{var LG=parent&&parent.skywireLog;if(LG&&LG.subscribe){LG.subscribe(function(lvl,args){var m=[].map.call(args||[],String).join(" ");if(/\[wallet\]|skysocks|resolve-proxy|electrum|dmsg:\/\//i.test(m))plog(m.slice(0,220));});plog("● attached to visor log");}}catch(e){}
function fetchProxies(cb){var v=visor();if(!v||!v.fetchDmsg){plog("● no in-tab visor here — open the wallet in the browser-tab (wasm) visor to list servers");return;}
plog("● fetching skysocks servers from service discovery…");
Promise.resolve(v.fetchDmsg("sd.dmsg","GET","/api/services?type=proxy",null)).then(function(r){
var list=[];try{list=JSON.parse((r&&typeof r.body==="string")?r.body:new TextDecoder().decode(r.body))||[];}catch(e){}
cb(list.filter(function(s){return /^[0-9a-f]{66}$/i.test(String(s.address||"").split(":")[0]);}));
}).catch(function(e){plog("● SD fetch failed: "+String((e&&e.message)||e));});}
$("psrv").onclick=function(){fetchProxies(function(list){
var sel=$("psel");sel.innerHTML='<option value="">— '+list.length+' skysocks servers — pick one —</option>';
list.forEach(function(s){var pk=String(s.address).split(":")[0];var geo=(s.geo&&s.geo.country)?" · "+s.geo.country:"";var o=document.createElement("option");o.value=pk;o.textContent=pk.slice(0,8)+"…"+geo+(s.version?" · "+s.version:"");sel.appendChild(o);});
$("pselrow").style.display="";plog("● "+list.length+" skysocks server(s) from SD — pick one to set the exit");});};
$("prnd").onclick=function(){fetchProxies(function(list){if(!list.length){plog("● no skysocks servers found");return;}
var s=list[Math.floor(Math.random()*list.length)];var pk=String(s.address).split(":")[0];
$("proxy").value=pk;plog("● 🎲 random exit → "+pk.slice(0,8)+"…"+((s.geo&&s.geo.country)?" · "+s.geo.country:"")+" (Apply to save)");});};
$("psel").onchange=function(){if(this.value){$("proxy").value=this.value;plog("● exit set → "+this.value.slice(0,8)+"… (Apply to save)");}};
$("apply").onclick=function(){var e=$("err");e.style.display="none";e.textContent="";
if(mode==="service"){var s=($("svc").value||"").trim();if(!s){e.textContent="Enter the wallet service address.";e.style.display="";return;}
set(K.mode,"service");set(K.svc,s);set(K.prim,s);}
else if(mode==="disk"){set(K.mode,"disk");set(K.dir,($("dir").value||"").trim());}
else{set(K.mode,"browser");
// Coin node config moved to skycoin-web's Settings → Nodes; clear any legacy
// panel-set node so that page (or the deployment mesh default) is authoritative.
set(K.nodes,"");set(K.prim,"");
var pxy=($("proxy").value||"").trim();set(K.proxy,pxy);set("skywire-upstream-proxy",pxy);}
try{parent.postMessage({type:"skywire-wallet-config"},"*");}catch(_){}
};
})();</script></body></html>`
