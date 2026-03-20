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
	nl                     []string
	navlinks               string
	htmltoplink            = "<a href='#top'>top of page</a>\n"
	htmlend                = "</pre></body></html>"
	htmlRewardPageTemplate = `
{{.Page.Content}}
`
	tmpl                 *htmpl.Template
	htmlPageTemplateData htmlTemplateData
)

func init() {
	// Main navigation links
	nl = append(nl, "  <a href='/'>fiber</a>")
	nl = append(nl, "  <a href='/skycoin-rewards'>skycoin rewards</a>")
	nl = append(nl, "  <a href='/stats'>network stats</a>")
	nl = append(nl, "  <a href='/stats/version-history'>version history</a>")
	nl = append(nl, "  <a href='/stats/bandwidth-history'>bandwidth history</a>")
	nl = append(nl, "  <a href='/stats/visor-bandwidth'>visor bandwidth</a>")
	nl = append(nl, "  <a href='/transport-graph'>transport graph</a>")

	// Log collection section
	nl = append(nl, "  <details><summary>log collection</summary><div class='dropdown'>")
	nl = append(nl, "    <a href='/log-collection'>overview</a>")
	nl = append(nl, "    <a href='/log-collection/tree'>survey index</a>")
	nl = append(nl, "    <a href='/log-collection/tplogs'>transport logs</a>")
	nl = append(nl, "  </div></details>")

	// External services section
	nl = append(nl, "  <details><summary>services</summary><div class='dropdown'>")
	nl = append(nl, "    <a href='"+strings.ReplaceAll(deployment.Prod.UptimeTracker, "http://", "https://")+"/uptimes?v=v2'>uptime tracker</a>")
	nl = append(nl, "    <a href='"+strings.ReplaceAll(deployment.Prod.AddressResolver, "http://", "https://")+"'>address resolver</a>")
	nl = append(nl, "    <a href='"+strings.ReplaceAll(deployment.Prod.TransportDiscovery, "http://", "https://")+"/all-transports'>transport discovery</a>")
	nl = append(nl, "  </div></details>")

	// DMSG discovery section
	nl = append(nl, "  <details><summary>dmsg</summary><div class='dropdown'>")
	nl = append(nl, "    <a href='"+strings.ReplaceAll(deployment.Prod.DmsgDiscovery, "http://", "https://")+"/dmsg-discovery/entries'>entries</a>")
	nl = append(nl, "    <a href='"+strings.ReplaceAll(deployment.Prod.DmsgDiscovery, "http://", "https://")+"/dmsg-discovery/all_servers'>all servers</a>")
	nl = append(nl, "    <a href='"+strings.ReplaceAll(deployment.Prod.DmsgDiscovery, "http://", "https://")+"/dmsg-discovery/available_servers'>available servers</a>")
	nl = append(nl, "  </div></details>")

	nl = append(nl, "  <a href='/login'>login</a>")
	nl = append(nl, "\n<br>\n")
	navlinks = strings.Join(nl, "")

}

type htmlTemplateData struct {
	Title   string
	Page    string
	Content htmpl.HTML
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
	htmlPageTemplateData1 := htmlPageTemplateData
	//nolint:gosec
	htmlPageTemplateData1.Content = htmpl.HTML(skywireVersion + "<br>" + skycoinlogohtml + "<br>" + mainnetRulesHTML)
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
<title title='{{.Page.Title}}'>{{.Page.Title}}</title>
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
