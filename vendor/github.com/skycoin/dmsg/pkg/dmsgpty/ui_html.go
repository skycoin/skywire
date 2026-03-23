// Package dmsgpty pkg/dmsgpty/ui_html.go
package dmsgpty

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"
)

//go:embed term.html.gz
var termHTMLGz []byte

// writeTermHTML returns raw, uncompressed file data.
func writeTermHTML(w io.Writer) (int64, error) {
	gz, err := gzip.NewReader(bytes.NewBuffer(termHTMLGz))
	if err != nil {
		return 0, err
	}

	return io.Copy(w, io.LimitReader(gz, 10<<20)) // cap decompression to 10MB
}
