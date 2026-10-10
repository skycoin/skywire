package charts

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/httputil"
)

// Range is one of the windows a page can show.
type Range struct {
	Name string
	Span time.Duration
	// Hourly reads the hourly means instead of the raw samples.
	Hourly bool
	// Bucket averages the samples down to this width.
	Bucket time.Duration
}

// Ranges are the windows every page offers, the first being the default.
var Ranges = []Range{
	{Name: "24h", Span: 24 * time.Hour, Bucket: Interval},
	{Name: "7d", Span: 7 * 24 * time.Hour, Bucket: 30 * time.Minute},
	{Name: "30d", Span: 30 * 24 * time.Hour, Hourly: true, Bucket: 2 * time.Hour},
	{Name: "1y", Span: 365 * 24 * time.Hour, Hourly: true, Bucket: 24 * time.Hour},
}

// Frame reads r's window from st, ending at now. The window starts at the
// first sample when the history is shorter than the range.
func (r Range) Frame(ctx context.Context, st Store, now time.Time) (*Frame, time.Time, error) {
	from := now.Add(-r.Span)
	samples, err := st.Range(ctx, from, now.Add(time.Second), r.Hourly)
	if err != nil {
		return nil, from, err
	}
	if r.Bucket > Interval {
		samples = Bucket(samples, r.Bucket)
	}
	if len(samples) > 0 && samples[0].At.After(from) {
		from = samples[0].At
	}
	return NewFrame(samples, r.Bucket), from, nil
}

// Table is a plain table shown after the charts.
type Table struct {
	Title string
	Note  string
	Head  []string
	Rows  [][]string
	// Marks, when set, gives a row a color to stand out in. Empty leaves it plain.
	Marks []string
	// Tall keeps a long table to a scrolling box with its header in view.
	Tall bool
}

// Content is what a page shows for one range.
type Content struct {
	Charts []Chart
	Tables []Table
}

// Page serves a service's charts as one self-contained HTML document.
type Page struct {
	Title string
	// About is a line under the title, such as the service's public key.
	About string
	// Links are other pages of the service, shown under the title.
	Links []Link
	Build func(ctx context.Context, r Range, now time.Time) (Content, error)
	// Stats, when set, adds the service's process and traffic charts after
	// Build's, read from Store.
	Stats *ServiceStats
	Store Store
	// Log, when set, records why a render failed; the client only sees a 503.
	Log logrus.FieldLogger

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	at   time.Time
	body []byte
	// gz is body gzipped, once per render rather than once per request.
	gz []byte
}

const cacheFor = time.Minute

// ServeHTTP renders the page for ?range=, from a copy at most a minute old.
func (p *Page) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	serveRendered(w, r, p.render, p.Log)
}

// serveRendered answers r with what render gives for its ?range=.
func serveRendered(w http.ResponseWriter, r *http.Request, render func(context.Context, Range) (cached, error), log logrus.FieldLogger) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rg := Ranges[0]
	for _, x := range Ranges {
		if x.Name == r.URL.Query().Get("range") {
			rg = x
		}
	}
	c, err := render(r.Context(), rg)
	if err != nil {
		if log != nil {
			log.WithError(err).WithField("range", rg.Name).Warn("charts page render failed")
		}
		http.Error(w, "charts unavailable", http.StatusServiceUnavailable)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=60")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; img-src data:")
	h.Set("X-Content-Type-Options", "nosniff")
	body := c.body
	if len(c.gz) > 0 && r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		body = c.gz
	}
	_, _ = w.Write(body) //nolint:errcheck
}

func (p *Page) render(ctx context.Context, rg Range) (cached, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.cache[rg.Name]; ok && time.Since(c.at) < cacheFor {
		return c, nil
	}
	now := time.Now().UTC()
	content, starts, err := p.content(ctx, rg, now)
	if err != nil {
		return cached{}, err
	}
	var b bytes.Buffer
	p.write(&b, rg, now, content, starts)
	if p.cache == nil {
		p.cache = map[string]cached{}
	}
	c := newCached(b.Bytes())
	p.cache[rg.Name] = c
	return c, nil
}

// content is what the page shows for rg, and when the service started.
func (p *Page) content(ctx context.Context, rg Range, now time.Time) (Content, []Start, error) {
	content, err := p.Build(ctx, rg, now)
	if err != nil {
		return Content{}, nil, err
	}
	if p.Stats != nil && p.Store != nil {
		if f, from, err := rg.Frame(ctx, p.Store, now); err == nil {
			content.Charts = append(content.Charts, p.Stats.Charts(f, from, now)...)
		}
	}
	var starts []Start
	if p.Store != nil {
		starts, _ = p.Store.Starts(ctx, now.Add(-HourlyRetention), now.Add(time.Second)) //nolint:errcheck
		for i := range content.Charts {
			content.Charts[i].Starts = starts
		}
	}
	return content, starts, nil
}

// newCached keeps body and, once, its gzip.
func newCached(body []byte) cached {
	c := cached{at: time.Now(), body: body}
	var z bytes.Buffer
	if gw, err := gzip.NewWriterLevel(&z, 5); err == nil {
		if _, err := gw.Write(c.body); err == nil && gw.Close() == nil {
			c.gz = z.Bytes()
		}
	}
	return c
}

func (p *Page) write(b *bytes.Buffer, rg Range, now time.Time, c Content, starts []Start) {
	writeHead(b, p.Title)
	writeIntro(b, p.About, starts, p.Links)
	writeNav(b, rg)
	writeContent(b, c, "c")
	writeFoot(b, now)
}

func writeHead(b *bytes.Buffer, title string) {
	t := html.EscapeString(title)
	fmt.Fprintf(b, "<!doctype html><html lang='en'><head><meta charset='utf-8'><meta name='viewport' content='width=device-width,initial-scale=1'><title>%s</title><style>%s</style></head><body><header><div><h1>%s</h1>", t, pageCSS, t)
}

// writeIntro writes the lines under a title: what it is, since when it has
// run, and its other pages.
func writeIntro(b *bytes.Buffer, about string, starts []Start, links []Link) {
	if about != "" {
		fmt.Fprintf(b, "<p class='about'>%s</p>", html.EscapeString(about))
	}
	if n := len(starts); n > 0 {
		s := starts[n-1]
		fmt.Fprintf(b, "<p class='about'>Running %s since <time data-t='%d'>%s UTC</time></p>", html.EscapeString(s.Label()), s.At.UnixMilli(), s.At.Format("2006-01-02 15:04"))
	}
	for _, l := range links {
		fmt.Fprintf(b, "<p class='links'><a href='%s'>%s</a></p>", html.EscapeString(l.Href), html.EscapeString(l.Name))
	}
}

// writeNav ends the header with the range links and opens main.
func writeNav(b *bytes.Buffer, rg Range) {
	b.WriteString("</div><nav>")
	for _, x := range Ranges {
		cls := ""
		if x.Name == rg.Name {
			cls = " class='on' aria-current='page'"
		}
		fmt.Fprintf(b, "<a href='?range=%s'%s>%s</a>", x.Name, cls, x.Name)
	}
	b.WriteString("</nav></header><main>")
}

// writeContent writes c's charts, with ids from prefix, then its tables.
func writeContent(b *bytes.Buffer, c Content, prefix string) {
	for i := range c.Charts {
		b.WriteString(c.Charts[i].SVG(fmt.Sprintf("%s%d", prefix, i)))
	}
	for _, t := range c.Tables {
		writeTable(b, t)
	}
}

func writeTable(b *bytes.Buffer, t Table) {
	fmt.Fprintf(b, "<section class='table'><h2>%s</h2>", html.EscapeString(t.Title))
	if t.Note != "" {
		fmt.Fprintf(b, "<p class='note'>%s</p>", html.EscapeString(t.Note))
	}
	if t.Tall {
		b.WriteString("<div class='scroll tall'><table><thead><tr>")
	} else {
		b.WriteString("<div class='scroll'><table><thead><tr>")
	}
	for _, h := range t.Head {
		fmt.Fprintf(b, "<th>%s</th>", html.EscapeString(h))
	}
	b.WriteString("</tr></thead><tbody>")
	for ri, row := range t.Rows {
		if ri < len(t.Marks) && t.Marks[ri] != "" {
			fmt.Fprintf(b, "<tr class='mark' style='--mark:%s'>", html.EscapeString(t.Marks[ri]))
		} else {
			b.WriteString("<tr>")
		}
		for _, cell := range row {
			fmt.Fprintf(b, "<td>%s</td>", html.EscapeString(cell))
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table></div></section>")
}

func writeFoot(b *bytes.Buffer, now time.Time) {
	fmt.Fprintf(b, "</main><footer>Sampled every %s and refreshed every minute while open. <span class='tz'>Times are UTC.</span> Rendered <time data-t='%d'>%s UTC</time>.</footer><script>%s</script></body></html>",
		strings.TrimSuffix(Interval.String(), "0s"), now.UnixMilli(), now.Format("2006-01-02 15:04"), pageJS)
}

const pageCSS = `:root{--bg:#f6f7f9;--card:#fff;--fg:#1d2330;--muted:#677084;--grid:#e3e6ec;--axis:#b9bfcb;--accent:#4e79a7;color-scheme:light}
@media (prefers-color-scheme:dark){:root{--bg:#0f1218;--card:#171b23;--fg:#e4e7ee;--muted:#8d95a6;--grid:#262c37;--axis:#3a4250;--accent:#7aa6d6;color-scheme:dark}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.45 system-ui,-apple-system,"Segoe UI",sans-serif}
header{display:flex;flex-wrap:wrap;gap:12px 24px;align-items:flex-end;justify-content:space-between;max-width:1100px;margin:0 auto;padding:28px 16px 8px}
h1{font-size:22px;margin:0;font-weight:650}.about{margin:4px 0 0;color:var(--muted);font:12px ui-monospace,monospace;overflow-wrap:anywhere}.links{margin:6px 0 0;font-size:13px}.links a{color:var(--accent);font-weight:600;text-decoration:none}
nav{display:flex;gap:4px;background:var(--card);padding:4px;border-radius:10px;box-shadow:0 1px 2px #0001}
nav a{color:var(--muted);text-decoration:none;padding:5px 12px;border-radius:7px;font-weight:550}nav a.on{background:var(--accent);color:#fff}
main{max-width:1100px;margin:0 auto;padding:8px 16px;display:grid;gap:16px}
.chart,.table{margin:0;background:var(--card);border-radius:14px;padding:16px 18px 12px;box-shadow:0 1px 3px #0000000f}
h2{font-size:15px;margin:0;font-weight:620}figcaption p,.note{margin:2px 0 0;color:var(--muted);font-size:12.5px}
.plot{position:relative;margin-top:10px}
.body{display:grid;grid-template-columns:minmax(0,1fr) 190px;gap:18px;align-items:center;margin-top:10px}.body .plot{margin-top:0}
@media (max-width:720px){.body{grid-template-columns:1fr}.pie{max-width:260px;width:100%;margin:0 auto}}
.pie svg{width:100%;max-width:150px;height:auto;margin:0 auto}.pie .pt{text-anchor:middle;font-size:24px;font-weight:650;fill:var(--fg)}.pie .pl{text-anchor:middle;font-size:14px;fill:var(--muted)}
.pie ul{list-style:none;padding:0;margin:8px 0 0;font-size:12px;height:114px;overflow-y:auto}.pie li{display:flex;align-items:center;gap:6px;height:19px;white-space:nowrap}.pie li span{overflow:hidden;text-overflow:ellipsis;min-width:0}.pie li b{margin-left:auto;flex-shrink:0;font-variant-numeric:tabular-nums;font-weight:600}.pie i{width:8px;height:8px;border-radius:2px;flex-shrink:0}
.pin{font-size:11px;color:var(--muted);margin:6px 0 0;visibility:hidden}figure.pinned .pin{visibility:visible}figure.pinned .cursor{stroke:var(--accent);stroke-dasharray:none;stroke-width:1.5}.plot svg{cursor:crosshair}svg{display:block;width:100%;height:auto;overflow:visible}
.grid,.xgrid{stroke:var(--grid);stroke-width:1}.axis{stroke:var(--axis)}
.ytick,.xtick{fill:var(--muted);font-size:11px}.ytick{text-anchor:end}.xtick{text-anchor:middle}
.area{fill-opacity:.55;stroke:none}.line{fill:none;stroke-width:1.8;stroke-linejoin:round;stroke-linecap:round}
.start line{stroke:#b07aa1;stroke-dasharray:2 3;stroke-width:1.2}.start circle{fill:#b07aa1}.tip .st{color:#b07aa1;font-weight:600}.cursor{stroke:var(--muted);stroke-dasharray:3 3;visibility:hidden}.empty{color:var(--muted);padding:40px 0;text-align:center}
.legend{list-style:none;display:flex;flex-wrap:wrap;gap:4px 16px;padding:0;margin:10px 0 0;font-size:12.5px}
.legend li{display:flex;align-items:center;gap:6px}.legend i{width:10px;height:10px;border-radius:3px}.legend b{font-weight:600;font-variant-numeric:tabular-nums}
.tip{position:absolute;top:8px;pointer-events:none;background:var(--card);border:1px solid var(--grid);border-radius:8px;padding:8px 10px;font-size:12px;box-shadow:0 4px 14px #0002;min-width:150px;z-index:2}
.tip div{display:flex;justify-content:space-between;gap:14px}.tip div span{display:flex;align-items:center;gap:6px}.tip i{width:8px;height:8px;border-radius:2px}
.tip .t{color:var(--muted);margin-bottom:4px}.tip b{font-variant-numeric:tabular-nums;font-weight:600}
.scroll{overflow-x:auto;margin-top:10px}.scroll.tall{max-height:28rem;overflow-y:auto}.scroll.tall thead th{position:sticky;top:0;background:var(--card)}table{border-collapse:collapse;width:100%;font-size:12.5px}
th,td{text-align:left;padding:6px 10px;border-bottom:1px solid var(--grid);white-space:nowrap}th{color:var(--muted);font-weight:550}
td:first-child{font-family:ui-monospace,monospace;font-size:11.5px}
tr.mark td{background:color-mix(in srgb,var(--mark) 13%,transparent)}tr.mark td:first-child{box-shadow:inset 3px 0 0 var(--mark)}
.legend-t i{display:block;width:10px;height:10px;border-radius:3px}.legend-t td:first-child{width:14px;padding-right:0}
.legend-t td:nth-child(2),.mono{font-family:ui-monospace,monospace;font-size:11.5px}td.num,th.num{text-align:right;font-variant-numeric:tabular-nums}
footer{max-width:1100px;margin:0 auto;padding:12px 16px 32px;color:var(--muted);font-size:12px}
section.svc{display:grid;gap:16px;padding-top:16px;border-top:1px solid var(--grid)}
h2.svc{font-size:18px}
section.svc>.note{margin:0;color:var(--muted);font-size:13px}`

const pageJS = `(function(){
var fT=new Intl.DateTimeFormat(undefined,{hour:'2-digit',minute:'2-digit',hourCycle:'h23'}),fD=new Intl.DateTimeFormat(undefined,{month:'short',day:'numeric'}),
fDT=new Intl.DateTimeFormat(undefined,{month:'short',day:'numeric',hour:'2-digit',minute:'2-digit',hourCycle:'h23'}),fM=new Intl.DateTimeFormat(undefined,{month:'short'}),fMY=new Intl.DateTimeFormat(undefined,{month:'short',year:'numeric'});
function lab(d,i){return d.dt||!d.t?d.l[i]:fDT.format(d.t[i])}
function axis(svg,d){if(d.dt||!d.f||d.to<=d.f)return;var ax=svg.querySelector('.axis');if(!ax)return;
svg.querySelectorAll('.xtick,.xgrid').forEach(function(e){e.remove()});
var g=d.g,span=d.to-d.f,H=36e5,steps=[1,2,3,6,12,24,48,168,336,672,1344],step=steps[steps.length-1]*H,k,t,ticks=[];
for(k=0;k<steps.length;k++)if(span/(steps[k]*H)<=8){step=steps[k]*H;break}
t=new Date(d.f);
if(step>=672*H){var every=Math.ceil((Math.floor(span/(720*H))+1)/8);t=new Date(t.getFullYear(),t.getMonth(),1);
while(t.getTime()<=d.to){if(t.getTime()>=d.f)ticks.push([t.getTime(),t.getMonth()===0?fMY.format(t):fM.format(t)]);t=new Date(t.getFullYear(),t.getMonth()+every,1)}}
else if(step>=24*H){var days=step/(24*H);t=new Date(t.getFullYear(),t.getMonth(),t.getDate());
while(t.getTime()<=d.to){if(t.getTime()>=d.f)ticks.push([t.getTime(),fD.format(t)]);t=new Date(t.getFullYear(),t.getMonth(),t.getDate()+days)}}
else{var hs=step/H;t.setMinutes(0,0,0);while(t.getHours()%hs)t=new Date(t.getTime()+H);
while(t.getTime()<=d.to){if(t.getTime()>=d.f)ticks.push([t.getTime(),t.getHours()===0?fD.format(t):fT.format(t)]);t=new Date(t.getTime()+step)}}
ticks.forEach(function(k){var x=(g[0]+(k[0]-d.f)/span*g[1]).toFixed(1),ns='http://www.w3.org/2000/svg',l=document.createElementNS(ns,'line'),tx=document.createElementNS(ns,'text');
l.setAttribute('class','xgrid');l.setAttribute('x1',x);l.setAttribute('x2',x);l.setAttribute('y1',g[2]);l.setAttribute('y2',g[2]+g[3]);
tx.setAttribute('class','xtick');tx.setAttribute('x',x);tx.setAttribute('y',g[4]-8);tx.textContent=k[1];svg.insertBefore(l,ax);svg.insertBefore(tx,ax)})}
function zone(){var z=Intl.DateTimeFormat().resolvedOptions().timeZone||'local';document.querySelectorAll('.tz').forEach(function(e){e.textContent='Times are in your time zone, '+z+'.'});
document.querySelectorAll('time[data-t]').forEach(function(e){e.textContent=fDT.format(+e.getAttribute('data-t'))})}

var hovering=0;
function esc(s){return String(s).replace(/[&<>"']/g,function(c){return '&#'+c.charCodeAt(0)+';'})}
function last(d){for(var i=d.x.length-1;i>=0;i--){if(d.s.some(function(q){return q.v[i]}))return i}return -1}
function arc(R,r,a,b){var C=Math.cos,S=Math.sin,l=b-a>Math.PI?1:0,p=function(x){return (100+x).toFixed(2)};
return 'M'+p(R*C(a))+' '+p(R*S(a))+'A'+R+' '+R+' 0 '+l+' 1 '+p(R*C(b))+' '+p(R*S(b))+'L'+p(r*C(b))+' '+p(r*S(b))+'A'+r+' '+r+' 0 '+l+' 0 '+p(r*C(a))+' '+p(r*S(a))+'Z'}
function pie(el,d,i,pinned){
if(!el)return;var items=[],tot=0,h0='';
d.s.forEach(function(q){if(!q.r)return;var v=q.r[i];if(v==null||v<=0)return;items.push({q:q,v:v});tot+=v});
if(!tot)h0='<circle cx="100" cy="100" r="76" fill="none" stroke="var(--grid)" stroke-width="32"/>';
var h='<svg viewBox="0 0 200 200" role="img">'+h0,a=-Math.PI/2;
items.forEach(function(it){var f=it.v/tot,b=Math.min(a+f*2*Math.PI,a+2*Math.PI-0.0001);
h+='<path d="'+arc(92,60,a,b)+'" fill="'+it.q.c+'"><title>'+esc(it.q.n)+' '+esc(it.q.v[i])+' ('+(f*100).toFixed(1)+'%)</title></path>';a+=f*2*Math.PI});
var rs=d.s.filter(function(q){return q.r}),c=d.s[0].n==='total'?d.s[0].v[i]:(rs.length===1?rs[0].v[i]:'');
h+='<text x="100" y="98" class="pt">'+esc(c||'')+'</text><text x="100" y="122" class="pl">'+esc(lab(d,i))+'</text></svg><ul>';
rs.map(function(q){return {q:q,v:q.r[i]}}).sort(function(x,y){return (y.v||0)-(x.v||0)}).forEach(function(it){
var t=tot&&it.v>0?' title="'+(it.v/tot*100).toFixed(1)+'%"':'';
h+='<li'+t+'><i style="background:'+it.q.c+'"></i><span>'+esc(it.q.n)+'</span><b>'+esc(it.q.v[i]||'')+'</b></li>'});
el.innerHTML=h+'</ul><p class="pin">Pinned. Click the same point to release.</p>'}
function init(){document.querySelectorAll('figure.chart').forEach(function(f){
var s=f.querySelector('script'),svg=f.querySelector('.plot svg'),tip=f.querySelector('.tip'),cur=f.querySelector('.cursor'),pe=f.querySelector('.pie');
if(!s||!svg)return;var d=JSON.parse(s.textContent),pin=null,end=last(d);axis(svg,d);
function near(e){var r=svg.getBoundingClientRect(),x=(e.clientX-r.left)/r.width*d.w,b=0,bd=1e9;
for(var i=0;i<d.x.length;i++){var k=Math.abs(d.x[i]-x);if(k<bd){bd=k;b=i}}return b}
function mark(i){if(i<0){cur.style.visibility='hidden';return}cur.setAttribute('x1',d.x[i]);cur.setAttribute('x2',d.x[i]);cur.style.visibility='visible'}
function readout(b){var r=svg.getBoundingClientRect(),h='<div class=t>'+esc(lab(d,b))+'</div>',n=0;
d.s.forEach(function(q){if(!q.v[b])return;n++;h+='<div><span>'+(q.c?'<i style="background:'+q.c+'"></i>':'')+esc(q.n)+'</span><b>'+esc(q.v[b])+'</b></div>'});
var hw=d.x.length>1?Math.abs(d.x[1]-d.x[0])/2:8;(d.d||[]).forEach(function(m){if(Math.abs(m.x-d.x[b])>hw)return;n++;h+='<div class=st>Started '+esc(m.v)+' at '+esc(fDT.format(m.t))+'</div>'});
if(!n){tip.hidden=true;return}tip.innerHTML=h;tip.hidden=false;
var px=d.x[b]/d.w*r.width,tw=tip.offsetWidth;tip.style.left=(px+12+tw>r.width?px-12-tw:px+12)+'px'}
if(end>=0)pie(pe,d,end,false);
svg.addEventListener('mouseenter',function(){hovering++});
svg.addEventListener('mouseleave',function(){hovering=Math.max(0,hovering-1);tip.hidden=true;
if(pin==null){mark(-1);if(end>=0)pie(pe,d,end,false)}else{mark(pin);pie(pe,d,pin,true)}});
svg.addEventListener('mousemove',function(e){if(!d.x.length)return;var b=near(e);readout(b);mark(b);if(pin==null)pie(pe,d,b,false)});
svg.addEventListener('click',function(e){if(!d.x.length)return;var b=near(e);pin=pin===b?null:b;
f.classList.toggle('pinned',pin!=null);pie(pe,d,b,pin!=null)})})}
zone();init();
setInterval(function(){if(document.hidden||hovering>0||document.querySelector('figure.pinned'))return;
fetch(location.href,{cache:'no-store'}).then(function(r){return r.ok?r.text():null}).then(function(t){if(!t)return;
var n=new DOMParser().parseFromString(t,'text/html'),m=n.querySelector('main'),ft=n.querySelector('footer');if(!m)return;
document.querySelector('main').replaceWith(m);if(ft)document.querySelector('footer').replaceWith(ft);zone();init()}).catch(function(){})},60000);
})();
`

// Link points to another page of the service.
type Link struct {
	Name string
	Href string
}

// Extra is another page a service serves next to its charts.
type Extra struct {
	Path    string
	Handler http.Handler
}

// Serve answers plain HTTP on addr with the page at /, any extra pages at
// their paths, and 404 for every other path, until ctx ends.
func Serve(ctx context.Context, addr string, page http.Handler, extra ...Extra) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           httputil.CompressMin(httputil.CompressMinBytes, 5)(rootOnly(page, extra...)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		_ = srv.Close() //nolint:errcheck
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func rootOnly(page http.Handler, extra ...Extra) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/{$}", page)
	for _, e := range extra {
		mux.Handle(e.Path, e.Handler)
	}
	return mux
}
