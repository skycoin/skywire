package progressive

import "encoding/base64"

// Mirror is the sequences publishing what the program shows as structure:
// an HTML fragment — headings, lists, links, a table — that the host keeps
// beside the cells for assistive technology, which reads a grid of
// characters badly. Send it again whenever what is shown changes; an empty
// fragment withdraws it. The host keeps only plain structure: no scripts,
// no styles, no forms, links only to web addresses.
func Mirror(html []byte) []string {
	var seqs []string
	for len(html) > 0 || seqs == nil {
		n := min(len(html), shipChunk)
		seqs = append(seqs, osc("mirror;"+b64json(map[string]bool{"more": n < len(html)})+";"+base64.StdEncoding.EncodeToString(html[:n])))
		html = html[n:]
	}
	return seqs
}
