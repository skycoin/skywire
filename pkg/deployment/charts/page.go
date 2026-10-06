package charts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
	"sync"
	"time"
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
	Build func(ctx context.Context, r Range, now time.Time) (Content, error)

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	at   time.Time
	body []byte
}

const cacheFor = time.Minute

// ServeHTTP renders the page for ?range=, from a copy at most a minute old.
func (p *Page) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	body, err := p.render(r.Context(), rg)
	if err != nil {
		http.Error(w, "charts unavailable", http.StatusServiceUnavailable)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=60")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:")
	h.Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(body) //nolint:errcheck
}

func (p *Page) render(ctx context.Context, rg Range) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.cache[rg.Name]; ok && time.Since(c.at) < cacheFor {
		return c.body, nil
	}
	now := time.Now().UTC()
	content, err := p.Build(ctx, rg, now)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	p.write(&b, rg, now, content)
	if p.cache == nil {
		p.cache = map[string]cached{}
	}
	p.cache[rg.Name] = cached{at: time.Now(), body: b.Bytes()}
	return b.Bytes(), nil
}

func (p *Page) write(b *bytes.Buffer, rg Range, now time.Time, c Content) {
	title := html.EscapeString(p.Title)
	fmt.Fprintf(b, "<!doctype html><html lang='en'><head><meta charset='utf-8'><meta name='viewport' content='width=device-width,initial-scale=1'><title>%s</title><style>%s</style></head><body><header><div><h1>%s</h1>", title, pageCSS, title)
	if p.About != "" {
		fmt.Fprintf(b, "<p class='about'>%s</p>", html.EscapeString(p.About))
	}
	b.WriteString("</div><nav>")
	for _, x := range Ranges {
		cls := ""
		if x.Name == rg.Name {
			cls = " class='on' aria-current='page'"
		}
		fmt.Fprintf(b, "<a href='?range=%s'%s>%s</a>", x.Name, cls, x.Name)
	}
	b.WriteString("</nav></header><main>")
	for i := range c.Charts {
		b.WriteString(c.Charts[i].SVG(fmt.Sprintf("c%d", i)))
	}
	for _, t := range c.Tables {
		fmt.Fprintf(b, "<section class='table'><h2>%s</h2>", html.EscapeString(t.Title))
		if t.Note != "" {
			fmt.Fprintf(b, "<p class='note'>%s</p>", html.EscapeString(t.Note))
		}
		b.WriteString("<div class='scroll'><table><thead><tr>")
		for _, h := range t.Head {
			fmt.Fprintf(b, "<th>%s</th>", html.EscapeString(h))
		}
		b.WriteString("</tr></thead><tbody>")
		for _, row := range t.Rows {
			b.WriteString("<tr>")
			for _, cell := range row {
				fmt.Fprintf(b, "<td>%s</td>", html.EscapeString(cell))
			}
			b.WriteString("</tr>")
		}
		b.WriteString("</tbody></table></div></section>")
	}
	fmt.Fprintf(b, "</main><footer>Sampled every %s. Times are UTC. Rendered %s.</footer><script>%s</script></body></html>",
		strings.TrimSuffix(Interval.String(), "0s"), now.Format("2006-01-02 15:04"), pageJS)
}

const pageCSS = `:root{--bg:#f6f7f9;--card:#fff;--fg:#1d2330;--muted:#677084;--grid:#e3e6ec;--axis:#b9bfcb;--accent:#4e79a7;color-scheme:light}
@media (prefers-color-scheme:dark){:root{--bg:#0f1218;--card:#171b23;--fg:#e4e7ee;--muted:#8d95a6;--grid:#262c37;--axis:#3a4250;--accent:#7aa6d6;color-scheme:dark}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.45 system-ui,-apple-system,"Segoe UI",sans-serif}
header{display:flex;flex-wrap:wrap;gap:12px 24px;align-items:flex-end;justify-content:space-between;max-width:1100px;margin:0 auto;padding:28px 16px 8px}
h1{font-size:22px;margin:0;font-weight:650}.about{margin:4px 0 0;color:var(--muted);font:12px ui-monospace,monospace;overflow-wrap:anywhere}
nav{display:flex;gap:4px;background:var(--card);padding:4px;border-radius:10px;box-shadow:0 1px 2px #0001}
nav a{color:var(--muted);text-decoration:none;padding:5px 12px;border-radius:7px;font-weight:550}nav a.on{background:var(--accent);color:#fff}
main{max-width:1100px;margin:0 auto;padding:8px 16px;display:grid;gap:16px}
.chart,.table{margin:0;background:var(--card);border-radius:14px;padding:16px 18px 12px;box-shadow:0 1px 3px #0000000f}
h2{font-size:15px;margin:0;font-weight:620}figcaption p,.note{margin:2px 0 0;color:var(--muted);font-size:12.5px}
.plot{position:relative;margin-top:10px}svg{display:block;width:100%;height:auto;overflow:visible}
.grid,.xgrid{stroke:var(--grid);stroke-width:1}.axis{stroke:var(--axis)}
.ytick,.xtick{fill:var(--muted);font-size:11px}.ytick{text-anchor:end}.xtick{text-anchor:middle}
.area{fill-opacity:.55;stroke:none}.line{fill:none;stroke-width:1.8;stroke-linejoin:round;stroke-linecap:round}
.cursor{stroke:var(--muted);stroke-dasharray:3 3;visibility:hidden}.empty{color:var(--muted);padding:40px 0;text-align:center}
.legend{list-style:none;display:flex;flex-wrap:wrap;gap:4px 16px;padding:0;margin:10px 0 0;font-size:12.5px}
.legend li{display:flex;align-items:center;gap:6px}.legend i{width:10px;height:10px;border-radius:3px}.legend b{font-weight:600;font-variant-numeric:tabular-nums}
.tip{position:absolute;top:8px;pointer-events:none;background:var(--card);border:1px solid var(--grid);border-radius:8px;padding:8px 10px;font-size:12px;box-shadow:0 4px 14px #0002;min-width:150px;z-index:2}
.tip div{display:flex;justify-content:space-between;gap:14px}.tip div span{display:flex;align-items:center;gap:6px}.tip i{width:8px;height:8px;border-radius:2px}
.tip .t{color:var(--muted);margin-bottom:4px}.tip b{font-variant-numeric:tabular-nums;font-weight:600}
.scroll{overflow-x:auto;margin-top:10px}table{border-collapse:collapse;width:100%;font-size:12.5px}
th,td{text-align:left;padding:6px 10px;border-bottom:1px solid var(--grid);white-space:nowrap}th{color:var(--muted);font-weight:550}
td:first-child{font-family:ui-monospace,monospace;font-size:11.5px}
.legend-t i{display:block;width:10px;height:10px;border-radius:3px}.legend-t td:first-child{width:14px;padding-right:0}
.legend-t td:nth-child(2),.mono{font-family:ui-monospace,monospace;font-size:11.5px}td.num,th.num{text-align:right;font-variant-numeric:tabular-nums}
footer{max-width:1100px;margin:0 auto;padding:12px 16px 32px;color:var(--muted);font-size:12px}`

const pageJS = `document.querySelectorAll('figure.chart').forEach(function(f){
var s=f.querySelector('script'),svg=f.querySelector('svg'),tip=f.querySelector('.tip'),cur=f.querySelector('.cursor');
if(!s||!svg)return;var d=JSON.parse(s.textContent);
function hide(){tip.hidden=true;cur.style.visibility='hidden'}
svg.addEventListener('mouseleave',hide);
svg.addEventListener('mousemove',function(e){var r=svg.getBoundingClientRect(),x=(e.clientX-r.left)/r.width*d.w,b=0,bd=1e9;
for(var i=0;i<d.x.length;i++){var k=Math.abs(d.x[i]-x);if(k<bd){bd=k;b=i}}
if(!d.x.length)return;var h='<div class=t>'+d.l[b]+'</div>',n=0;
d.s.forEach(function(q){if(!q.v[b])return;n++;h+='<div><span>'+(q.c?'<i style="background:'+q.c+'"></i>':'')+q.n.replace(/[<&]/g,'')+'</span><b>'+q.v[b]+'</b></div>'});
if(!n)return hide();tip.innerHTML=h;tip.hidden=false;cur.setAttribute('x1',d.x[b]);cur.setAttribute('x2',d.x[b]);cur.style.visibility='visible';
var px=d.x[b]/d.w*r.width,tw=tip.offsetWidth;tip.style.left=(px+12+tw>r.width?px-12-tw:px+12)+'px'})});`

// Serve answers plain HTTP on addr with the page at / and 404 for every
// other path, until ctx ends.
func Serve(ctx context.Context, addr string, page http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           rootOnly(page),
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

func rootOnly(page http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/{$}", page)
	return mux
}
