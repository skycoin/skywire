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
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:")
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
	b.WriteString("<main><div id='types'></div><div id='wrap'><canvas id='c'></canvas><div id='tip' hidden></div></div>")
	b.WriteString("<p class='sub'>Drag to pan, scroll to zoom, click a visor to keep its transports highlighted. Each line is a pair of visors joined by at least one transport of a checked type.</p></main>")
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
#wrap{position:relative;background:var(--card);border-radius:14px;box-shadow:0 1px 3px #0000000f;overflow:hidden}
canvas{display:block;width:100%;height:min(78vh,900px);cursor:grab;touch-action:none}canvas.drag{cursor:grabbing}
#tip{position:absolute;top:10px;left:10px;max-width:calc(100% - 20px);background:var(--card);border:1px solid var(--grid);border-radius:9px;padding:9px 11px;font-size:12px;box-shadow:0 4px 14px #0002;pointer-events:none}
#tip .k{font:11.5px ui-monospace,monospace;overflow-wrap:anywhere}#tip div{display:flex;justify-content:space-between;gap:16px}#tip b{font-variant-numeric:tabular-nums}`

const graphJS = `(function(){
var G=JSON.parse(document.getElementById('g').textContent),n=G.k.length;
var raw=atob(G.e),m=raw.length/5,A=new Uint16Array(m),B=new Uint16Array(m),M=new Uint8Array(m);
for(var i=0;i<m;i++){var o=i*5;A[i]=raw.charCodeAt(o)|raw.charCodeAt(o+1)<<8;B[i]=raw.charCodeAt(o+2)|raw.charCodeAt(o+3)<<8;M[i]=raw.charCodeAt(o+4)}
var COL={dmsg:'#4e79a7',stcpr:'#f28e2b',sudph:'#e15759',stcp:'#76b7b2',webrtc:'#59a14f',squicr:'#edc948',swtr:'#b07aa1',swsr:'#ff9da7',other:'#bab0ac'},PAL=['#9c755f','#86bcb6','#d37295','#a0cbe8'];
var on=G.t.map(function(){return true}),col=G.t.map(function(t,i){return COL[t]||PAL[i%PAL.length]}),cnt=G.t.map(function(){return 0});
for(i=0;i<m;i++)for(var t=0;t<G.t.length;t++)if(M[i]>>t&1)cnt[t]++;
var tb=document.getElementById('types');G.t.forEach(function(t,i){var l=document.createElement('label');
l.innerHTML='<input type="checkbox" checked><i style="background:'+col[i]+'"></i>'+t+' <b>'+cnt[i].toLocaleString()+'</b>';
l.firstChild.onchange=function(e){on[i]=e.target.checked;draw()};tb.appendChild(l)});
var cv=document.getElementById('c'),cx=cv.getContext('2d'),tip=document.getElementById('tip'),dpr=window.devicePixelRatio||1;
var S=1,OX=0,OY=0,hover=-1,sel=-1,W=0,H=0;
function css(v){return getComputedStyle(document.documentElement).getPropertyValue(v).trim()}
function fit(){var r=cv.getBoundingClientRect();W=r.width;H=r.height;cv.width=W*dpr;cv.height=H*dpr;
var x0=1e9,x1=-1e9,y0=1e9,y1=-1e9;for(var i=0;i<n;i++){x0=Math.min(x0,G.x[i]);x1=Math.max(x1,G.x[i]);y0=Math.min(y0,G.y[i]);y1=Math.max(y1,G.y[i])}
S=0.92*Math.min(W/(x1-x0||1),H/(y1-y0||1));OX=W/2-S*(x0+x1)/2;OY=H/2-S*(y0+y1)/2}
function sx(i){return OX+S*G.x[i]}function sy(i){return OY+S*G.y[i]}
function first(mk){for(var t=0;t<G.t.length;t++)if(on[t]&&mk>>t&1)return t;return -1}
function draw(){cx.setTransform(dpr,0,0,dpr,0,0);cx.clearRect(0,0,W,H);var focus=sel>=0?sel:hover;
cx.lineWidth=1;cx.globalAlpha=focus>=0?0.025:Math.min(0.35,Math.max(0.03,600/m));
for(var t=0;t<G.t.length;t++){if(!on[t])continue;cx.strokeStyle=col[t];cx.beginPath();
for(var i=0;i<m;i++){if(first(M[i])!==t)continue;cx.moveTo(sx(A[i]),sy(A[i]));cx.lineTo(sx(B[i]),sy(B[i]))}cx.stroke()}
if(focus>=0){cx.globalAlpha=0.75;cx.lineWidth=1.2;for(t=0;t<G.t.length;t++){if(!on[t])continue;cx.strokeStyle=col[t];cx.beginPath();
for(i=0;i<m;i++){if((A[i]!==focus&&B[i]!==focus)||first(M[i])!==t)continue;cx.moveTo(sx(A[i]),sy(A[i]));cx.lineTo(sx(B[i]),sy(B[i]))}cx.stroke()}}
cx.globalAlpha=0.9;cx.fillStyle=css('--fg');cx.beginPath();for(i=0;i<n;i++){var r=Math.max(1.2,Math.min(4.5,0.5+Math.sqrt(G.d[i])/6));cx.moveTo(sx(i)+r,sy(i));cx.arc(sx(i),sy(i),r,0,6.2832)}cx.fill();
if(focus>=0){cx.globalAlpha=1;cx.fillStyle=css('--accent');cx.beginPath();cx.arc(sx(focus),sy(focus),7,0,6.2832);cx.fill()}cx.globalAlpha=1}
function near(px,py){var b=-1,bd=100;for(var i=0;i<n;i++){var dx=sx(i)-px,dy=sy(i)-py,d=dx*dx+dy*dy;if(d<bd){bd=d;b=i}}return b}
function info(i){if(i<0){tip.hidden=true;return}var per=G.t.map(function(){return 0}),peers=0;
for(var e=0;e<m;e++){if(A[e]!==i&&B[e]!==i)continue;peers++;for(var t=0;t<G.t.length;t++)if(M[e]>>t&1)per[t]++}
var h='<div class=k>'+G.k[i]+'</div><div><span>transports</span><b>'+G.d[i]+'</b></div><div><span>peers</span><b>'+peers+'</b></div>';
G.t.forEach(function(t,j){if(per[j])h+='<div><span>'+t+' peers</span><b>'+per[j]+'</b></div>'});tip.innerHTML=h;tip.hidden=false}
var drag=null;cv.addEventListener('pointerdown',function(e){drag={x:e.clientX,y:e.clientY,ox:OX,oy:OY,moved:false};cv.setPointerCapture(e.pointerId);cv.classList.add('drag')});
cv.addEventListener('pointermove',function(e){var r=cv.getBoundingClientRect();if(drag){var dx=e.clientX-drag.x,dy=e.clientY-drag.y;if(Math.abs(dx)+Math.abs(dy)>3)drag.moved=true;OX=drag.ox+dx;OY=drag.oy+dy;draw();return}
var h=near(e.clientX-r.left,e.clientY-r.top);if(h!==hover){hover=h;if(sel<0)info(h);draw()}});
cv.addEventListener('pointerup',function(e){var r=cv.getBoundingClientRect();cv.classList.remove('drag');if(drag&&!drag.moved){sel=near(e.clientX-r.left,e.clientY-r.top);info(sel>=0?sel:hover);draw()}drag=null});
cv.addEventListener('pointerleave',function(){hover=-1;if(sel<0)info(-1);draw()});
cv.addEventListener('wheel',function(e){e.preventDefault();var r=cv.getBoundingClientRect(),px=e.clientX-r.left,py=e.clientY-r.top,f=Math.exp(-e.deltaY*0.0015);OX=px-(px-OX)*f;OY=py-(py-OY)*f;S*=f;draw()},{passive:false});
document.getElementById('find').addEventListener('input',function(e){var q=e.target.value.trim().toLowerCase();if(q.length<6)return;
for(var i=0;i<n;i++)if(G.k[i].indexOf(q)===0){sel=i;info(i);OX=W/2-S*G.x[i];OY=H/2-S*G.y[i];draw();return}});
window.addEventListener('resize',function(){fit();draw()});fit();draw()})();`
