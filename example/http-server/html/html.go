// Package html example/http-server/html/html.go
package html

import (
	"embed"
	"io"
	"text/template"
)

//go:embed *
var files embed.FS

var (
	homepage = parse("homepage.html")
)

// HomepageParams contains data to be shown on the homepage
type HomepageParams struct {
	Title   string
	Message string
}

// Homepage renders the homepage
func Homepage(w io.Writer, p HomepageParams) error {
	return homepage.Execute(w, p)
}

func parse(file string) *template.Template {
	return template.Must(
		template.New("layout.html").ParseFS(files, "layout.html", file))
}

// FS returns embed.FS
func FS() embed.FS {
	return files
}
