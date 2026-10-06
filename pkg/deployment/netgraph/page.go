package netgraph

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RefreshEvery is how long a layout is served before it is recomputed.
const RefreshEvery = 15 * time.Minute

// Page serves the transport graph. Source returns the current transports,
// or an error while they are not known yet.
type Page struct {
	Title  string
	Back   string
	Source func(ctx context.Context) ([]Link, error)

	mu    sync.Mutex
	at    time.Time
	body  []byte
	graph *Graph
}

func (p *Page) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := p.render(r.Context())
	if err != nil {
		http.Error(w, "the transport graph is not ready yet", http.StatusServiceUnavailable)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=300")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'self' 'unsafe-inline' 'wasm-unsafe-eval'; connect-src 'self'; img-src data: blob:")
	h.Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(body) //nolint:errcheck
}

func (p *Page) render(ctx context.Context) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.body != nil && time.Since(p.at) < RefreshEvery {
		return p.body, nil
	}
	links, err := p.Source(ctx)
	if err != nil {
		if p.body != nil {
			return p.body, nil
		}
		return nil, err
	}
	var prev map[string][2]float64
	if p.graph != nil {
		prev = p.graph.Positions()
	}
	g := Build(links, prev)
	p.graph, p.at = g, time.Now()
	p.body = p.write(g, p.at)
	return p.body, nil
}

// packEdges is five bytes an edge: both ends as little-endian uint16, then
// the type mask. It is a fifth of the size of the same edges as JSON.
func packEdges(edges []Edge) string {
	b := make([]byte, 5*len(edges))
	for i, e := range edges {
		binary.LittleEndian.PutUint16(b[5*i:], e.A)
		binary.LittleEndian.PutUint16(b[5*i+2:], e.B)
		b[5*i+4] = e.Mask
	}
	return base64.StdEncoding.EncodeToString(b)
}

func (p *Page) write(g *Graph, at time.Time) []byte {
	type data struct {
		Types []string  `json:"t"`
		Nodes []string  `json:"k"`
		X     []float32 `json:"x"`
		Y     []float32 `json:"y"`
		Deg   []int     `json:"d"`
		Edges string    `json:"e"`
	}
	d := data{Types: g.Types, Nodes: g.Nodes, Deg: g.Degree, Edges: packEdges(g.Edges)}
	for i := range g.Nodes {
		d.X = append(d.X, float32(math.Round(g.X[i]*1e4)/1e4))
		d.Y = append(d.Y, float32(math.Round(g.Y[i]*1e4)/1e4))
	}
	js, err := json.Marshal(d)
	if err != nil {
		return nil
	}
	var b bytes.Buffer
	title := html.EscapeString(p.Title)
	fmt.Fprintf(&b, "<!doctype html><html lang='en'><head><meta charset='utf-8'><meta name='viewport' content='width=device-width,initial-scale=1'><title>%s</title><style>%s</style></head><body>", title, graphCSS)
	fmt.Fprintf(&b, "<header><div><h1>%s</h1><p class='sub'>%d visors, %d visor pairs, %d transports. Laid out %s UTC.</p>",
		title, len(g.Nodes), len(g.Edges), g.Transports, at.UTC().Format("2006-01-02 15:04"))
	if p.Back != "" {
		fmt.Fprintf(&b, "<p class='sub'><a href='%s'>Charts</a></p>", html.EscapeString(p.Back))
	}
	b.WriteString("</div><input id='find' placeholder='Find a visor by public key' spellcheck='false' autocomplete='off'></header>")
	b.WriteString("<main><div id='types'></div><div class='bar'><label><input type='checkbox' id='physics'> Let the layout move</label><button id='fit'>Fit</button><span id='status'></span></div><div id='gl'></div><div id='tip' hidden></div>")
	b.WriteString("<p class='sub'>Drawn on the GPU by cosmos-go. Drag to pan, scroll to zoom, click a visor to keep its transports highlighted. Each line is a pair of visors joined by at least one transport of a checked type.</p></main>")
	fmt.Fprintf(&b, "<script type='application/json' id='g'>%s</script><script>%s</script></body></html>",
		strings.ReplaceAll(string(js), "</", "<\\/"), graphJS)
	return b.Bytes()
}

const graphCSS = `:root{--bg:#f6f7f9;--card:#fff;--fg:#1d2330;--muted:#677084;--grid:#e3e6ec;--accent:#4e79a7;color-scheme:light}
@media (prefers-color-scheme:dark){:root{--bg:#0f1218;--card:#171b23;--fg:#e4e7ee;--muted:#8d95a6;--grid:#262c37;--accent:#7aa6d6;color-scheme:dark}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.45 system-ui,-apple-system,"Segoe UI",sans-serif}
header{display:flex;flex-wrap:wrap;gap:12px 24px;align-items:flex-end;justify-content:space-between;max-width:1400px;margin:0 auto;padding:24px 16px 8px}
h1{font-size:22px;margin:0;font-weight:650}.sub{margin:4px 0 0;color:var(--muted);font-size:12.5px}.sub a{color:var(--accent);font-weight:600;text-decoration:none}
#find{width:min(560px,100%);padding:8px 10px;border-radius:9px;border:1px solid var(--grid);background:var(--card);color:var(--fg);font:12px ui-monospace,monospace}
main{max-width:1400px;margin:0 auto;padding:8px 16px 32px}
#types{display:flex;flex-wrap:wrap;gap:6px 16px;margin:4px 0 10px;font-size:13px}#types label{display:flex;align-items:center;gap:6px;cursor:pointer}
#types i{width:12px;height:3px;border-radius:2px}#types b{color:var(--muted);font-weight:500;font-variant-numeric:tabular-nums}
#gl{position:relative;height:min(78vh,900px);border-radius:14px;overflow:hidden;background:#0b1020;box-shadow:0 1px 3px #0000000f}
.bar{display:flex;flex-wrap:wrap;align-items:center;gap:8px 16px;margin:0 0 10px;font-size:13px}.bar label{display:flex;gap:6px;align-items:center;cursor:pointer}
.bar button{padding:4px 12px;border-radius:7px;border:1px solid var(--grid);background:var(--card);color:var(--fg);cursor:pointer}#status{color:var(--muted)}
#tip{position:fixed;z-index:10;max-width:min(560px,calc(100% - 20px));background:var(--card);border:1px solid var(--grid);border-radius:9px;padding:9px 11px;font-size:12px;box-shadow:0 4px 14px #0002;pointer-events:none}
#tip .k{font:11.5px ui-monospace,monospace;overflow-wrap:anywhere}#tip div{display:flex;justify-content:space-between;gap:16px}#tip b{font-variant-numeric:tabular-nums}`

const graphJS = `(function(){
var G=JSON.parse(document.getElementById('g').textContent),n=G.k.length;
var raw=atob(G.e),m=raw.length/5,A=new Uint32Array(m),B=new Uint32Array(m),M=new Uint8Array(m);
for(var i=0;i<m;i++){var o=i*5;A[i]=raw.charCodeAt(o)|raw.charCodeAt(o+1)<<8;B[i]=raw.charCodeAt(o+2)|raw.charCodeAt(o+3)<<8;M[i]=raw.charCodeAt(o+4)}
var COL={dmsg:'#4e79a7',stcpr:'#f28e2b',sudph:'#e15759',stcp:'#76b7b2',webrtc:'#59a14f',squicr:'#edc948',swtr:'#b07aa1',swsr:'#ff9da7',other:'#bab0ac'},PAL=['#9c755f','#86bcb6','#d37295','#a0cbe8'];
var on=G.t.map(function(){return true}),col=G.t.map(function(t,i){return COL[t]||PAL[i%PAL.length]}),cnt=G.t.map(function(){return 0});
for(i=0;i<m;i++)for(var t=0;t<G.t.length;t++)if(M[i]>>t&1)cnt[t]++;
var status=document.getElementById('status'),tip=document.getElementById('tip'),sel=-1;
function say(s){status.textContent=s}
var adj=null;function neighbors(){if(adj)return adj;adj=[];for(var i=0;i<n;i++)adj.push([]);for(var e=0;e<m;e++){adj[A[e]].push(e);adj[B[e]].push(e)}return adj}
function first(mk){for(var t=0;t<G.t.length;t++)if(on[t]&&mk>>t&1)return t;return -1}
var x0=1e9,x1=-1e9,y0=1e9,y1=-1e9;for(i=0;i<n;i++){x0=Math.min(x0,G.x[i]);x1=Math.max(x1,G.x[i]);y0=Math.min(y0,G.y[i]);y1=Math.max(y1,G.y[i])}
var SPACE=8192,pad=SPACE*0.1,sc=(SPACE-2*pad)/Math.max(x1-x0,y1-y0,1e-9);
var pos=new Float32Array(n*2),size=new Float32Array(n),pcol=new Array(n);
for(i=0;i<n;i++){pos[2*i]=pad+(G.x[i]-x0)*sc;pos[2*i+1]=pad+(G.y[i]-y0)*sc;size[i]=Math.min(7,2+Math.sqrt(G.d[i])/6);pcol[i]='#dfe5ef'}
function payload(){var idx=[],lc=[];for(var e=0;e<m;e++){var t=first(M[e]);if(t<0)continue;idx.push(A[e],B[e]);lc.push(col[t])}
var w=new Float32Array(lc.length);w.fill(0.6);return {positions:pos,pointColors:pcol,pointSizes:size,links:new Float32Array(idx),linkColors:lc,linkWidths:w,grouped:!physics.checked,boundaries:[]}}
var gl=null,physics=document.getElementById('physics');
function show(){if(!gl)return;gl.setData(payload());if(sel>=0)gl.selectIdx(sel)}
var tb=document.getElementById('types');G.t.forEach(function(t,i){var l=document.createElement('label');
l.innerHTML='<input type="checkbox" checked><i style="background:'+col[i]+'"></i>'+t+' <b>'+cnt[i].toLocaleString()+'</b>';
l.firstChild.onchange=function(e){on[i]=e.target.checked;show()};tb.appendChild(l)});
physics.onchange=function(){if(gl){gl.setPhysics(physics.checked);if(!physics.checked)show()}};
function info(i,x,y){if(i<0){tip.hidden=true;return}var per=G.t.map(function(){return 0}),es=neighbors()[i];
es.forEach(function(e){for(var t=0;t<G.t.length;t++)if(M[e]>>t&1)per[t]++});
var h='<div class=k>'+G.k[i]+'</div><div><span>transports</span><b>'+G.d[i]+'</b></div><div><span>peers</span><b>'+es.length+'</b></div>';
G.t.forEach(function(t,j){if(per[j])h+='<div><span>'+t+' peers</span><b>'+per[j]+'</b></div>'});
tip.innerHTML=h;tip.hidden=false;if(x||y){tip.style.left=Math.min(x+14,innerWidth-tip.offsetWidth-8)+'px';tip.style.top=Math.min(y+14,innerHeight-tip.offsetHeight-8)+'px'}else{tip.style.left='24px';tip.style.top='24px'}}
function onEvent(kind,i,x,y){switch(kind){case 'click':sel=i;info(i,x,y);break;case 'bgclick':sel=-1;info(-1);break;
case 'over':info(i,x,y);break;case 'out':if(sel<0)info(-1);if(gl)gl.selectIdx(sel);break;case 'simend':say('');break}}
document.getElementById('find').addEventListener('input',function(e){var q=e.target.value.trim().toLowerCase();if(q.length<6||!gl)return;
for(var i=0;i<n;i++)if(G.k[i].indexOf(q)===0){sel=i;gl.focusIndex(i);info(i,0,0);return}});
document.getElementById('fit').onclick=function(){if(gl)gl.fit()};
var probe=document.createElement('canvas');if(!(probe.getContext('webgl2')||probe.getContext('webgl'))){say('This browser has no WebGL, which the graph needs.');return}
say('Loading the WebGL engine. It is the skywire wasm module, about 35 MB, and the browser keeps it after the first visit.');
var s=document.createElement('script');s.src='graph/engine.js';s.onerror=function(){say('The WebGL engine did not load.')};
s.onload=function(){var go=new Go();go.argv=['skywire','desk-host','--role','netview'];
WebAssembly.instantiateStreaming(fetch('graph/engine.wasm'),go.importObject).then(function(r){go.run(r.instance);var tries=0;
(function wait(){if(window.tpvizGL&&window.tpvizGL.ready){gl=window.tpvizGL;if(!gl.init('gl',onEvent)){say('The WebGL engine could not start.');return}
show();say('');return}if(++tries>200){say('The WebGL engine did not start.');return}setTimeout(wait,25)})()}).catch(function(err){say('The WebGL engine failed: '+err)})};
document.head.appendChild(s)})();
`
