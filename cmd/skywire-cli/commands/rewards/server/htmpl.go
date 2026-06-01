// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/htmpl.go
package clirewardsserver

import (
	"bytes"
	"fmt"
	htmpl "html/template"
	"net/http"
	"strings"

	"github.com/bitfield/script"
	"github.com/gin-gonic/gin"

	"github.com/skycoin/skywire/deployment"
)

var (
	// html snippets
	navlinks               string
	htmltoplink            = "<a href='#top'>top of page</a>\n"
	htmlend                = "</pre></body></html>"
	htmlRewardPageTemplate string
	tmpl                   *htmpl.Template
	htmlPageTemplateData   htmlTemplateData
)

func init() {
	// Navigation bar — matches the base template (htmlMainPageTemplate) structure.
	// Includes inline CSS so pages not using the base template still render correctly.
	navCSS := `<style>
nav{display:flex;flex-wrap:wrap;gap:8px;align-items:center;padding:4px 0;}
nav a{padding:4px 8px;white-space:nowrap;color:#3399FF;}
nav a:visited{color:#FF00FF;}
nav details{display:inline-flex;position:relative;}
nav details summary{list-style:none;padding:4px 8px;cursor:pointer;color:#3399FF;}
nav details summary::-webkit-details-marker{display:none;}
nav details summary::after{content:' ▶';font-size:8px;}
nav details[open] summary::after{content:' ▼';}
nav .dropdown{position:absolute;top:100%;left:0;background:#222;border-radius:4px;z-index:100;min-width:max-content;padding:4px 0;border:1px solid #333;}
nav .dropdown a{display:block;padding:4px 12px;}
</style>`

	navInner := `<nav>
  <a href='/'>fiber</a>
  <a href='/skycoin-rewards'>skycoin rewards</a>
  <a href='/stats'>network stats</a>
  <a href='/stats/version-history'>version history</a>
  <a href='/stats/bandwidth-history'>bandwidth history</a>
  <a href='/stats/visor-bandwidth'>visor bandwidth</a>
  <a href='/transport-graph'>transport graph</a>
  <details><summary>logs</summary><div class='dropdown'>
    <a href='/log-collection'>overview</a>
    <a href='/log-collection/tree'>survey index</a>
    <a href='/log-collection/tplogs'>transport logs</a>
  </div></details>
  <details><summary>services</summary><div class='dropdown'>
    <a href='` + strings.ReplaceAll(deployment.Prod.UptimeTracker, "http://", "https://") + `/uptimes?v=v2'>uptime tracker</a>
    <a href='` + strings.ReplaceAll(deployment.Prod.AddressResolver, "http://", "https://") + `'>address resolver</a>
    <a href='` + strings.ReplaceAll(deployment.Prod.TransportDiscovery, "http://", "https://") + `/all-transports'>transport discovery</a>
  </div></details>
  <details><summary>dmsg</summary><div class='dropdown'>
    <a href='` + strings.ReplaceAll(deployment.Prod.DmsgDiscovery, "http://", "https://") + `/dmsg-discovery/entries'>entries</a>
    <a href='` + strings.ReplaceAll(deployment.Prod.DmsgDiscovery, "http://", "https://") + `/dmsg-discovery/all_servers'>all servers</a>
    <a href='` + strings.ReplaceAll(deployment.Prod.DmsgDiscovery, "http://", "https://") + `/dmsg-discovery/available_servers'>available servers</a>
  </div></details>
  <a href='/login'>login</a>
  <details><summary>resources</summary><div class='dropdown'>
    <a title='Skywire APT repository — install via apt-get' href='https://deb.theskywirenetwork.net'>apt repo (deb)</a>
    <a title='Skywire Network blog' href='https://blog.theskywirenetwork.net'>blog</a>
  </div></details>
  <details><summary>community</summary><div class='dropdown'>
    <a title='@skywire telegram' href='https://t.me/skywire'>skywire telegram</a>
    <a title='@skywire_reward telegram' href='https://t.me/skywire_reward'>reward notifications</a>
  </div></details>
</nav>`

	navlinks = navCSS + navInner

	// htmlRewardPageTemplate is rendered inside the base htmlMainPageTemplate
	// which already provides the HTML document wrapper and navigation.
	htmlRewardPageTemplate = `{{.Page.Content}}`

}

type htmlTemplateData struct {
	Title       string
	Description string
	Canonical   string
	OGImage     string
	Page        string
	Content     htmpl.HTML
}

// withCanonical returns a copy of the template data with canonical URL and OG image set
// based on the canonicalDomain flag and the given request path.
func (d htmlTemplateData) withCanonical(path string) htmlTemplateData {
	if canonicalDomain != "" {
		base := strings.TrimRight(canonicalDomain, "/")
		d.Canonical = base + path
		d.OGImage = base + "/favicon.ico"
	}
	return d
}

// chunkedPageHead returns an HTML head for chunked (non-template) pages with
// SEO meta tags. Title and description should be page-specific.
// The path parameter is the request path (e.g. "/stats") for canonical URL generation.
func chunkedPageHead(title, description, path string) string {
	h := `<!doctype html><html lang='en'><head>` +
		`<meta charset='UTF-8'>` +
		`<meta name='viewport' content='width=device-width, initial-scale=1.0'>` +
		`<title>` + title + ` - Skywire Network</title>` +
		`<meta name='description' content='` + description + `'>` +
		`<meta property='og:title' content='` + title + ` - Skywire Network'>` +
		`<meta property='og:description' content='` + description + `'>` +
		`<meta property='og:type' content='website'>`
	if canonicalDomain != "" {
		canonical := strings.TrimRight(canonicalDomain, "/") + path
		h += `<link rel='canonical' href='` + canonical + `'>` +
			`<meta property='og:url' content='` + canonical + `'>` +
			`<meta property='og:image' content='` + strings.TrimRight(canonicalDomain, "/") + `/favicon.ico'>`
	}
	h += `<style type='text/css'>` +
		`a { color: #3399FF; } a:visited { color: #FF00FF; } ` +
		`pre { font-family:Courier New; font-size:10pt; white-space:pre-wrap; word-wrap:break-word; } ` +
		`body { background-color:black; color:white; }` +
		`</style></head><body><pre>`
	return h
}

const htmlFrontPageTemplate = `
┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┬─┐┌─┐┬ ┬┌─┐┬─┐┌┬┐┌─┐
└─┐├┴┐└┬┘││││├┬┘├┤   ├┬┘├┤ │││├─┤├┬┘ ││└─┐
└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘  ┴└─└─┘└┴┘┴ ┴┴└──┴┘└─┘<br>
{{.Page.Content}}
`

func mainPage(c *gin.Context) {
	c.Writer.Header().Set("Server", "")
	tmpl0, err1 := tmpl.Clone()
	if err1 != nil {
		fmt.Println("Error cloning template:", err1)
	}
	_, err1 = tmpl0.New("this").Parse(htmlFrontPageTemplate)
	if err1 != nil {
		fmt.Println("Error parsing Front Page template:", err1)
	}
	tmpl := tmpl0

	mainnetRulesHTML, _ := script.Exec(`skywire cli reward rules -l`).String()              //nolint:errcheck,gosec
	skywireVersion, _ := script.Exec(`skywire -v`).Replace("skywire version ", "").String() //nolint:errcheck,gosec
	htmlPageTemplateData1 := htmlPageTemplateData.withCanonical("/")
	// Short orientation block above the mainnet-rules dump so first-
	// time visitors get a value-prop, an install entry point, and a
	// pointer to the eligibility rules — instead of being dropped
	// straight into a 200-line rules article. Keep this terse;
	// detailed rules live below.
	introHTML := `<div style='border:1px solid #333;padding:10px 14px;margin:8px 0;background:#1a1d24;border-radius:4px;'>` +
		`<b>Skywire</b> is a decentralized mesh network. Run a visor on any computer (Linux, ARM SBC, Windows, macOS) and earn <b>Skycoin</b> rewards for providing uptime and bandwidth to the network. 816,000 SKY are distributed annually across two reward pools (presence + bandwidth) to all eligible visors.<br><br>` +
		`<b>Get started:</b> <a href='https://deb.theskywirenetwork.net'>install via APT</a> &middot; ` +
		`<a href='/skycoin-rewards'>view reward history</a> &middot; ` +
		`<a href='https://blog.theskywirenetwork.net'>blog</a> &middot; ` +
		`<a href='https://t.me/skywire'>community</a><br>` +
		`<span style='color:#94a3b8;font-size:9pt;'>Full eligibility rules below. Network statistics at <a href='/stats'>/stats</a>.</span>` +
		`</div>`
	//nolint:gosec
	htmlPageTemplateData1.Content = htmpl.HTML(skywireVersion + "<br>" + skycoinlogohtml + "<br>" + introHTML + mainnetRulesHTML)
	tmplData := map[string]interface{}{
		"Page": htmlPageTemplateData1,
	}
	var result bytes.Buffer
	err = tmpl.Execute(&result, tmplData)
	if err != nil {
		fmt.Println("error: ", err)
		c.Writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Write((bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(bytes.Replace(result.Bytes(), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1), []byte("\n\n"), []byte("\n"), -1))) //nolint:errcheck,gosec
}

var htmlMainPageTemplate = `
{{ $page := .Page }}<!doctype html><html lang='en'>
{{template "head" .}}
<body title='' style='background-color:black;color:white;'>
<a id='top' class='anchor' aria-hidden='true' href='#top'></a><header>
  <nav>
  <a href='/'>fiber</a>
  <a href='/skycoin-rewards'>skycoin rewards</a>
  <a href='/stats'>network stats</a>
  <a href='/stats/version-history'>version history</a>
  <a href='/stats/bandwidth-history'>bandwidth history</a>
  <a href='/stats/visor-bandwidth'>visor bandwidth</a>
  <a href='/transport-graph'>transport graph</a>
  <details><summary>logs</summary><div class='dropdown'>
    <a href='/log-collection'>overview</a>
    <a href='/log-collection/tree'>survey index</a>
    <a href='/log-collection/tplogs'>transport logs</a>
  </div></details>
  <details><summary>services</summary><div class='dropdown'>
    <a href='` + strings.ReplaceAll(deployment.Prod.UptimeTracker, "http://", "https://") + `/uptimes?v=v2'>uptime tracker</a>
    <a href='` + strings.ReplaceAll(deployment.Prod.AddressResolver, "http://", "https://") + `'>address resolver</a>
    <a href='` + strings.ReplaceAll(deployment.Prod.TransportDiscovery, "http://", "https://") + `/all-transports'>transport discovery</a>
  </div></details>
  <details><summary>dmsg</summary><div class='dropdown'>
    <a href='` + strings.ReplaceAll(deployment.Prod.DmsgDiscovery, "http://", "https://") + `/dmsg-discovery/entries'>entries</a>
    <a href='` + strings.ReplaceAll(deployment.Prod.DmsgDiscovery, "http://", "https://") + `/dmsg-discovery/all_servers'>all servers</a>
    <a href='` + strings.ReplaceAll(deployment.Prod.DmsgDiscovery, "http://", "https://") + `/dmsg-discovery/available_servers'>available servers</a>
  </div></details>
  <a href='/login'>login</a>
  <details><summary>resources</summary><div class='dropdown'>
    <a title='Skywire APT repository — install via apt-get' href='https://deb.theskywirenetwork.net'>apt repo (deb)</a>
    <a title='Skywire Network blog' href='https://blog.theskywirenetwork.net'>blog</a>
  </div></details>
  <details><summary>community</summary><div class='dropdown'>
    <a title='@skywire telegram' href='https://t.me/skywire'>skywire telegram</a>
    <a title='@skywire_reward telegram' href='https://t.me/skywire_reward'>reward notifications</a>
  </div></details>
  </nav>
</header>
<br>
<pre>
<main>
{{template "this" .}}
</main>
</pre>
</body></html>
`

var htmlHeadTemplate = `<head>
<meta charset='UTF-8'>
<meta name='viewport' content='width=device-width, initial-scale=1.0, maximum-scale=4.9,'>
<title>{{.Page.Title}} - Skywire Network</title>
{{if .Page.Description}}<meta name='description' content='{{.Page.Description}}'>
<meta property='og:description' content='{{.Page.Description}}'>{{end}}
<meta property='og:title' content='{{.Page.Title}} - Skywire Network'>
<meta property='og:type' content='website'>
{{if .Page.Canonical}}<link rel='canonical' href='{{.Page.Canonical}}'>
<meta property='og:url' content='{{.Page.Canonical}}'>
<meta property='og:image' content='{{.Page.OGImage}}'>{{end}}
<style type='text/css'>
a {
		color: #3399FF;
}
a:visited {
		color: #FF00FF;
}
pre {
	font-family:mononokiregular;
	font-size:10pt;
	white-space: pre-wrap;
	word-wrap: break-word;
	overflow-wrap: break-word;
}
.af_line {
	color: gray;
	text-decoration: none;
}
.column {
	float: left;
	width: 30%;
	padding: 10px;
}
.row:after {
	content: '';
	display: table;
	clear: both;
}
/* Mobile responsive styles */
nav {
	display: flex;
	flex-wrap: wrap;
	gap: 8px;
	align-items: center;
}
nav a {
	padding: 4px 8px;
	white-space: nowrap;
}
/* Details/summary styling for collapsible nav sections */
nav details {
	display: inline-flex;
	position: relative;
}
nav details summary {
	list-style: none;
	padding: 4px 8px;
	cursor: pointer;
}
nav details summary::-webkit-details-marker {
	display: none;
}
nav details summary::after {
	content: ' ▶';
	font-size: 8px;
}
nav details[open] summary::after {
	content: ' ▼';
}
nav .dropdown {
	position: absolute;
	top: 100%;
	left: 0;
	background: #222;
	border-radius: 4px;
	z-index: 100;
	min-width: max-content;
	padding: 4px 0;
	border: 1px solid #333;
}
nav .dropdown a {
	display: block;
	padding: 4px 12px;
}
table {
	max-width: 100%;
	overflow-x: auto;
	display: block;
}
@media (max-width: 768px) {
	pre {
		font-size: 9pt;
		padding: 0 5px;
	}
	nav {
		font-size: 11px;
	}
	.column {
		width: 100%;
		float: none;
	}
	table {
		font-size: 10px;
	}
}
@media (max-width: 480px) {
	pre {
		font-size: 8pt;
	}
	nav {
		font-size: 10px;
		gap: 4px;
	}
	nav a {
		padding: 3px 5px;
	}
}
</style>
<style>
@font-face {
    font-family: 'mononokiregular';
    src: url(data:application/font-woff2;charset=utf-8;base64,` + MononokiWoff2 + `) format('woff2'),
         url(data:application/font-woff;charset=utf-8;base64,` + MononokiWoff + `) format('woff'),
         url('font/mononoki-regular-webfont.ttf') format('truetype');
    font-weight: normal;
    font-style: normal;
}
body {
	font-family:mononokiregular;
}
</style>
</head>`

// normalizeNewlines collapses consecutive blank lines into single newlines.
func normalizeNewlines(data []byte) []byte {
	doubleNL := []byte("\n\n")
	singleNL := []byte("\n")
	for bytes.Contains(data, doubleNL) {
		data = bytes.ReplaceAll(data, doubleNL, singleNL)
	}
	return data
}
